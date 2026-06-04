# node-app — seeded intermittent bug (dogfood for `/debug`)

A minimal Node HTTP service with a planted **intermittent** bug, used to exercise
the sherlog detective loop end to end.

## The symptom

`GET /me` *usually* returns `200 {user, token}`, but **intermittently** returns
`401 {error: "no token"}` for an already-authenticated user. It is flaky: it only
happens when a request lands inside a token-refresh window.

## Reproduce

```bash
node server.js        # terminal 1 — starts on http://127.0.0.1:3100
node load.js          # terminal 2 — hammers /me for 3s, prints ok/failed counts
```

You'll see a handful of `failed` (401) responses among many `ok` ones.

## The dogfood flow (what `/debug` should do)

1. `/debug` describing "GET /me intermittently 401s with 'no token'".
2. The skill opens a case and records **≥3 suspects**, e.g.
   - h1: a **race** between token refresh and the request (the real cause),
   - h2: a **stale cache** serving an expired token,
   - h3: connection/pool churn dropping the token.
3. It plants **discriminating probes** — at minimum one in `server.js` at the
   `/me` handler posting `{cachedToken, refreshing, t: Date.now()}`, and one in
   `refreshToken()` posting `{phase: "cleared"|"set", t}`. Together these split
   "race" (token null *while* `refreshing` true) from "stale cache" (token
   populated but old). Each probe is one fire-and-forget line and is
   `register_probe`'d.
4. **"the game is afoot"** → you run `node load.js` → `await_run` collects the
   evidence → you give the verdict (`reproduced`).
5. The summary shows the 401 path firing exactly when `refreshing=true` and
   `cachedToken=null` — h1 confirmed, h2/h3 killed with evidence notes.
6. **Fix:** refresh into a temp variable and swap atomically (never null the
   cache). Predict the null-window probe stops firing.
7. Re-run `load.js` → `fixed-check` run → the 401-path probe fires zero times →
   **"elementary."**
8. `debug_end` → remove both probe lines → `grep -rn "2218/log/<session>" .`
   returns nothing → **"case closed · all probes removed"**.

The seeded fix lives in `refreshToken()`: clearing `cachedToken = null` up front
is the write gap. A correct fix computes the new token first, then assigns once.
