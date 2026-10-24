// auth.go — ログイン・ログアウト・「ログインしているか」の確認
//
// しくみは original_app（第21章）と同じ。データベースがないので、変数で管理する:
//  1. ログインに成功したら、ランダムな合言葉（トークン）を作る
//  2. ブラウザには合言葉を Cookie で渡す（HttpOnly なので JavaScript からは読めない）
//  3. サーバーには合言葉を SHA-256 で変換した値（ハッシュ）だけを覚えておく
//  4. 次からのリクエストでは、Cookie の合言葉をハッシュにして照らし合わせる
//  5. ログアウトしたら覚えていたハッシュを消す → その合言葉はすぐに使えなくなる
//
// ログインできる人は .env の LOGIN_EMAIL / LOGIN_PASSWORD の1人だけ。
// サーバーを止めると、ログイン中の状態も消える（もう一度ログインが必要）。
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "memo_session"
	sessionLifetime   = 12 * time.Hour
	bcryptCost        = 12
	maxFailedLogins   = 5                // この回数続けて間違えたら…
	lockDuration      = 15 * time.Minute // …この時間ログインできなくする
	minPasswordLength = 10
)

// User は、ログインしている人の情報（パスワードのハッシュは入れない）
type User struct {
	Email string `json:"email"`
}

type session struct {
	email   string
	expires time.Time
}

var (
	authMu       sync.Mutex
	loginEmail   string
	passwordHash []byte
	failedLogins int
	lockedUntil  time.Time
	sessions     = map[[32]byte]session{} // キーは合言葉のハッシュ
)

// setupLoginUser は、起動時に .env のメールアドレスとパスワードを読み込む。
// パスワードはすぐに bcrypt のハッシュにして、元の文字列は覚えておかない。
func setupLoginUser(email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return errors.New("LOGIN_EMAIL と LOGIN_PASSWORD が設定されていません。.env.example を見て .env に書いてください")
	}
	if utf8.RuneCountInString(password) < minPasswordLength {
		return fmt.Errorf("LOGIN_PASSWORD は%d文字以上にしてください", minPasswordLength)
	}
	if len(password) > 72 { // bcrypt は72バイトより後ろを無視してしまうため
		return errors.New("LOGIN_PASSWORD が長すぎます（半角72文字まで）")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	loginEmail, passwordHash = email, h
	return nil
}

// ----- セッション（ログイン中の状態） -----

// startSession は、新しい合言葉を作って覚えておき、Cookie でブラウザに渡す
func startSession(w http.ResponseWriter, email string) error {
	buf := make([]byte, 32) // 256ビットのランダムな値（推測できない）
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	expires := time.Now().Add(sessionLifetime)

	authMu.Lock()
	for k, s := range sessions { // ついでに期限切れを片づける
		if s.expires.Before(time.Now()) {
			delete(sessions, k)
		}
	}
	sessions[sha256.Sum256([]byte(token))] = session{email: email, expires: expires}
	authMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                    // JavaScript から読めない
		SameSite: http.SameSiteStrictMode, // ほかのサイトからのリクエストには付けない
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// userFromRequest は、Cookie の合言葉から、ログインしている人を探す。いなければ nil
func userFromRequest(r *http.Request) *User {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	authMu.Lock()
	defer authMu.Unlock()
	s, ok := sessions[sha256.Sum256([]byte(c.Value))]
	if !ok || s.expires.Before(time.Now()) {
		return nil
	}
	return &User{Email: s.email}
}

// ----- 「ログインしている人だけ」にする -----

type ctxKey struct{}

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

// requireAuth は、ログインしていなければ 401 を返し、していれば次の処理に進む
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromRequest(r)
		if u == nil {
			clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "ログインしてください")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

// ----- API -----

// POST /api/login  {"email": "...", "password": "..."}
func login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	const wrong = "メールアドレスかパスワードが違います"

	authMu.Lock()
	if lockedUntil.After(time.Now()) {
		mins := int(time.Until(lockedUntil).Minutes()) + 1
		authMu.Unlock()
		writeError(w, http.StatusTooManyRequests,
			fmt.Sprintf("ログインの失敗が続いたため、ロックしています。%d分ほど待ってからお試しください", mins))
		return
	}
	authMu.Unlock()

	// メールアドレスが違っても、パスワードの照合は必ず行う（かかる時間でばれないように）
	emailOK := strings.ToLower(strings.TrimSpace(in.Email)) == loginEmail
	passwordOK := bcrypt.CompareHashAndPassword(passwordHash, []byte(in.Password)) == nil

	if !emailOK || !passwordOK {
		authMu.Lock()
		failedLogins++
		if failedLogins >= maxFailedLogins {
			failedLogins = 0
			lockedUntil = time.Now().Add(lockDuration)
		}
		authMu.Unlock()
		writeError(w, http.StatusUnauthorized, wrong)
		return
	}

	authMu.Lock()
	failedLogins = 0
	authMu.Unlock()

	if err := startSession(w, loginEmail); err != nil {
		writeError(w, http.StatusInternalServerError, "ログインに失敗しました")
		return
	}
	writeJSON(w, http.StatusOK, &User{Email: loginEmail})
}

// POST /api/logout
func logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		authMu.Lock()
		delete(sessions, sha256.Sum256([]byte(c.Value)))
		authMu.Unlock()
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/me … ログインしている人の情報
func getMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}
