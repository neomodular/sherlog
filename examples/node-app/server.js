// A tiny HTTP service with a SEEDED intermittent bug for dogfooding /debug.
//
// Symptom: GET /me intermittently returns 401 "no token" even for a freshly
// logged-in user. It is flaky — it only happens when a request races an
// in-flight token refresh.
//
// Root cause (the suspect the detective should confirm): refreshToken() clears
// the cached token BEFORE the new one is ready (a write gap), so a concurrent
// request reads null in that window. Rivals a real investigation should also
// consider: a stale cache, or pool/connection issues.
//
// No sherlog probes are present — the whole point of the dogfood is to add them
// live via /debug, watch the evidence, fix the race, verify, and clean up.

const http = require("http");

let cachedToken = "tok-initial";
let refreshing = false;

// Simulates an async token refresh. BUG: the cache is cleared synchronously up
// front, then repopulated only after the async delay — leaving a null window.
function refreshToken() {
  if (refreshing) return;
  refreshing = true;
  cachedToken = null; // <-- the write gap: cache is null until the timer fires
  setTimeout(() => {
    cachedToken = "tok-" + Date.now();
    refreshing = false;
  }, 25);
}

// Periodically refresh, opening the null window roughly every 50ms.
setInterval(refreshToken, 50);

const server = http.createServer((req, res) => {
  if (req.url === "/me") {
    if (!cachedToken) {
      res.writeHead(401, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "no token" }));
      return;
    }
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ user: "ada", token: cachedToken }));
    return;
  }
  res.writeHead(404);
  res.end();
});

const PORT = process.env.PORT || 3100;
server.listen(PORT, () => {
  console.log(`node-app listening on http://127.0.0.1:${PORT} (try /me)`);
});
