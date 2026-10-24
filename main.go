package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Memo は1件のメモを表す。
type Memo struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

// メモはデータベースを使わず、プログラム内の変数で管理する。
// サーバーを止めると消える。
var (
	memos  []Memo
	nextID = 1
	mu     sync.Mutex
)

const maxContentLength = 1000

func main() {
	godotenv.Load() // .env があれば読み込む

	if err := setupLoginUser(os.Getenv("LOGIN_EMAIL"), os.Getenv("LOGIN_PASSWORD")); err != nil {
		log.Fatal(err)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatal("ANTHROPIC_API_KEY が設定されていません。.env.example を見て .env に書いてください")
	}

	// ログイン・ログアウト
	http.HandleFunc("POST /api/login", login)
	http.HandleFunc("POST /api/logout", logout)
	http.HandleFunc("GET /api/me", requireAuth(getMe))

	// メモと AI 機能は、ログインした人だけが使える
	http.HandleFunc("GET /api/memos", requireAuth(listMemos))
	http.HandleFunc("POST /api/memos", requireAuth(createMemo))
	http.HandleFunc("POST /api/memos/summary", requireAuth(summarizeMemos))

	http.Handle("/", http.FileServer(http.Dir("static")))

	log.Println("http://localhost:8080 で起動しました")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// listMemos はメモを新しい順に返す。
func listMemos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, memosNewestFirst())
}

func memosNewestFirst() []Memo {
	mu.Lock()
	defer mu.Unlock()
	result := make([]Memo, len(memos))
	for i, m := range memos {
		result[len(memos)-1-i] = m
	}
	return result
}

func createMemo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if !readJSON(w, r, &req) {
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "メモを入力してください")
		return
	}
	if len([]rune(content)) > maxContentLength {
		writeError(w, http.StatusBadRequest, "メモは1000文字以内で入力してください")
		return
	}

	mu.Lock()
	memo := Memo{ID: nextID, Content: content, CreatedAt: time.Now()}
	nextID++
	memos = append(memos, memo)
	mu.Unlock()

	writeJSON(w, http.StatusCreated, memo)
}

// readJSON はリクエストの JSON を読む。失敗したら 400 を返して false を返す。
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "送られたデータが正しくありません")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
