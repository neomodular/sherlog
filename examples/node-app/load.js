// Drives /me in a tight loop to surface the intermittent 401. Run alongside
// server.js to reproduce the bug for a /debug `await_run`.
//
//   node server.js        # terminal 1
//   node load.js          # terminal 2

const http = require("http");

const PORT = process.env.PORT || 3100;
let ok = 0;
let failed = 0;

function hit() {
  http
    .get(`http://127.0.0.1:${PORT}/me`, (res) => {
      res.resume();
      if (res.statusCode === 200) ok++;
      else failed++;
    })
    .on("error", () => failed++);
}

const timer = setInterval(hit, 5);

setTimeout(() => {
  clearInterval(timer);
  console.log(`done: ${ok} ok, ${failed} failed (401s = the bug)`);
}, 3000);
