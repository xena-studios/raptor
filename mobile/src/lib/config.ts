// Where the app talks to. Production by default; a development build can
// point at a local Panel with EXPO_PUBLIC_API_URL and EXPO_PUBLIC_APP_URL
// (see mobile/README.md).
export const apiURL = (process.env.EXPO_PUBLIC_API_URL ?? "https://api.raptorpanel.net").replace(
  /\/$/,
  "",
);
export const appURL = (process.env.EXPO_PUBLIC_APP_URL ?? "https://app.raptorpanel.net").replace(
  /\/$/,
  "",
);

// How the Panel knows the app (auth.AppClients in the Panel): its client ID
// and where the web app sends the sign-in back to (the app's URL scheme).
export const clientId = "raptor-mobile";
export const redirectURI = "net.raptorpanel.app:/oauth";
