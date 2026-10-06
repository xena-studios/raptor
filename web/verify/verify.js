// Renders Turnstile and passes its token to the web app that embeds this
// page, and only to it.
(() => {
  // The app's origins: production, and the dev server.
  const apps = ["https://app.raptorpanel.net", "http://localhost:5173"];
  const params = new URLSearchParams(window.location.search);
  const sitekey = params.get("sitekey");
  const app = params.get("origin");
  if (!sitekey || !app || !apps.includes(app) || window.parent === window) return;

  const send = (token) => window.parent.postMessage({ type: "turnstile", token }, app);

  window.onTurnstileLoad = () => {
    const id = window.turnstile.render("#widget", {
      sitekey,
      theme: params.get("theme") === "light" ? "light" : "dark",
      callback: (token) => send(token),
      "expired-callback": () => send(""),
      "error-callback": () => send(""),
    });
    // A token works once: the app asks for a new one after using it.
    window.addEventListener("message", (e) => {
      if (e.origin === app && e.data && e.data.type === "turnstile.reset")
        window.turnstile.reset(id);
    });
  };
})();
