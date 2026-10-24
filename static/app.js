const loginView = document.getElementById("login-view");
const appView = document.getElementById("app-view");
const loginForm = document.getElementById("login-form");
const loginEmail = document.getElementById("login-email");
const loginPassword = document.getElementById("login-password");
const loginButton = document.getElementById("login-button");
const loginError = document.getElementById("login-error");
const logoutButton = document.getElementById("logout-button");

const form = document.getElementById("memo-form");
const input = document.getElementById("memo-input");
const submitButton = document.getElementById("submit-button");
const errorText = document.getElementById("error");
const list = document.getElementById("memo-list");
const empty = document.getElementById("empty");

const summaryButton = document.getElementById("summary-button");
const summaryResult = document.getElementById("summary-result");

// ----- 画面の切り替え -----

function showLogin() {
  appView.hidden = true;
  logoutButton.hidden = true;
  loginView.hidden = false;
  loginEmail.focus();
}

function showApp() {
  loginView.hidden = true;
  appView.hidden = false;
  logoutButton.hidden = false;
  loadMemos();
}

// API を呼び出す。ログインが切れていたら（401）ログイン画面に戻す
async function api(url, options) {
  const res = await fetch(url, options);
  if (res.status === 401 && url !== "/api/login") {
    showLogin();
    throw new Error("ログインしてください");
  }
  return res;
}

async function errorMessage(res, fallback) {
  const data = await res.json().catch(() => ({}));
  return data.error || fallback;
}

// ----- ログイン・ログアウト -----

loginForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  loginError.textContent = "";
  loginButton.disabled = true;
  try {
    const res = await api("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: loginEmail.value, password: loginPassword.value }),
    });
    if (!res.ok) {
      loginError.textContent = await errorMessage(res, "ログインに失敗しました");
      return;
    }
    loginPassword.value = "";
    showApp();
  } catch {
    loginError.textContent = "ログインに失敗しました";
  } finally {
    loginButton.disabled = false;
  }
});

logoutButton.addEventListener("click", async () => {
  await fetch("/api/logout", { method: "POST" }).catch(() => {});
  list.innerHTML = "";
  summaryResult.textContent = "「要約する」を押すと、登録したメモを AI が読んで3行にまとめます";
  summaryResult.classList.remove("has-result");
  showLogin();
});

// ----- メモ -----

// メモ一覧を取得して表示する
async function loadMemos() {
  try {
    const res = await api("/api/memos");
    if (!res.ok) throw new Error();
    renderMemos(await res.json());
  } catch {
    errorText.textContent = "メモの読み込みに失敗しました";
  }
}

function renderMemos(memos) {
  list.innerHTML = "";
  empty.hidden = memos.length > 0;

  for (const memo of memos) {
    const li = document.createElement("li");

    const content = document.createElement("p");
    content.className = "memo-content";
    content.textContent = memo.content;

    const date = document.createElement("div");
    date.className = "memo-date";
    date.textContent = new Date(memo.createdAt).toLocaleString("ja-JP");

    li.append(content, date);
    list.append(li);
  }
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  errorText.textContent = "";

  const content = input.value.trim();
  if (!content) {
    errorText.textContent = "メモを入力してください";
    return;
  }

  submitButton.disabled = true;
  try {
    const res = await api("/api/memos", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content }),
    });
    if (!res.ok) {
      errorText.textContent = await errorMessage(res, "登録に失敗しました");
      return;
    }
    input.value = "";
    await loadMemos();
  } catch {
    errorText.textContent = "登録に失敗しました";
  } finally {
    submitButton.disabled = false;
    input.focus();
  }
});

// ----- AI で3行に要約 -----

summaryButton.addEventListener("click", async () => {
  summaryButton.disabled = true;
  summaryResult.classList.remove("has-result", "is-error");
  summaryResult.textContent = "AI が要約しています…";
  try {
    const res = await api("/api/memos/summary", { method: "POST" });
    if (!res.ok) {
      summaryResult.classList.add("is-error");
      summaryResult.textContent = await errorMessage(res, "要約に失敗しました");
      return;
    }
    const data = await res.json();
    summaryResult.classList.add("has-result");
    summaryResult.textContent = data.summary;
  } catch {
    summaryResult.classList.add("is-error");
    summaryResult.textContent = "要約に失敗しました";
  } finally {
    summaryButton.disabled = false;
  }
});

// ----- 最初に、ログインしているかを確かめる -----

fetch("/api/me")
  .then((res) => (res.ok ? showApp() : showLogin()))
  .catch(showLogin);
