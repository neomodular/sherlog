# browser-page — seeded DOM/fetch bug (dogfood for `/debug`)

A single static HTML page with a planted bug, used to dogfood the sherlog loop in
the browser — the headline case for sherlog's preflight-free browser probes.

## The symptom

Open `index.html`, click **"Load profile"**. The name stays `—`. The network
call succeeds (200), but nothing renders.

## Reproduce

Open the file directly in a browser (`file://`) or serve it:

```bash
python3 -m http.server 8000   # then visit http://127.0.0.1:8000/
```

Click "Load profile". The `<span id="name">` never updates.

## The dogfood flow (what `/debug` should do)

1. `/debug` describing "clicking Load profile leaves the name blank though the
   request succeeds".
2. The skill records **≥3 suspects**, e.g.
   - h1: response **parsed wrong** — body read as text, `.name` read off a string
     (the real cause),
   - h2: the **request failed** / wrong status,
   - h3: wrong **DOM target** (`name` element id mismatch).
3. It plants **discriminating browser probes**, one fire-and-forget line each,
   **with no JSON `Content-Type`** so there is no CORS preflight — e.g. in the
   click handler:

   ```js
   fetch("http://127.0.0.1:2218/log/<session>/p1",
     {method:"POST", body: JSON.stringify({status: res.status, typeofParsed: typeof parsed, name: parsed.name})}).catch(()=>{})
   ```

   This single probe splits all three suspects: `status` rules h2 in/out,
   `typeofParsed: "string"` exposes h1, and a present-but-unrendered `name` would
   point at h3. Each probe is `register_probe`'d.
4. **"the game is afoot"** → you click the button → `await_run` → verdict
   `reproduced`.
5. Evidence: `status:200`, `typeofParsed:"string"`, `name:undefined` → h1
   confirmed, h2/h3 killed with notes.
6. **Fix:** `const parsed = await res.json();` (or `JSON.parse(await res.text())`).
   Predict the probe now shows `typeofParsed:"object"` and a real `name`.
7. Re-click → `fixed-check` run → probe shows the parsed object → **"elementary."**
8. `debug_end` → delete the probe line → `grep -rn "2218/log/<session>" .`
   returns nothing → **"case closed"**.

The seeded fix is on the `res.text()` line in `index.html`: reading the body as
text and then accessing `.name` on a string yields `undefined`. Parse the JSON.
