# Raptor for iOS and Android

Raptor's phone app ([#450](https://github.com/xena-studios/raptor/issues/450)): alerts as push notifications, the inbox, every server with its state, power (start, restart, stop), and backups. Everything else, and anything that needs a passkey, opens the web app.

Expo (SDK 58) and React Native, with Expo Router. Bundle ID and Android package: `net.raptorpanel.app`.

## How it works

- **Signing in** ([docs/PANEL.md](../docs/PANEL.md#the-phone-app)): the app opens `app.raptorpanel.net/authorize` in the system browser with a PKCE challenge; the person signs in there the usual way and taps Sign in; the browser comes back to `net.raptorpanel.app:/oauth` with a one-time code, which the app trades at `POST /oauth/token` for a 1-hour access token and a refresh token. The refresh token is kept in the Keychain or Keystore (`expo-secure-store`) and works once; refreshes go one at a time (`src/lib/auth.ts`).
- **The API** is the web app's Connect API with the access token as a bearer (`src/lib/transport.ts`), from the same protobufs (`src/gen`, made by `task gen`).
- **Push** (`src/lib/push.ts`): after signing in the app asks for notifications, takes the device's own push token from Apple or Google (not an Expo push token), and registers it (`RegisterPushDevice`), which makes the phone one of the person's alert channels. The Panel sends straight to APNs and FCM. Android has two channels, `alerts` and `urgent`. Tapping an alert opens its server.
- **App lock** (`src/lib/lock.ts`): optional, in Settings. Face ID (Touch ID, a fingerprint, or the passcode) when the app opens and when it comes back after a minute away; the app switcher sees only the logo while it's in the background. Turning it on checks Face ID works first; signing out turns it off.
- **Approving sign-ins** (`src/lib/approvals.ts`, `src/app/approve.tsx`): on accounts with an authenticator app, the phone can stand in for its codes. Turned on right after signing in (the inbox offers it) or in Settings; a 32-byte secret behind Face ID; a request (pushed, or shown on the inbox) asks for the number the browser shows. See [docs/PANEL.md](../docs/PANEL.md#phone-approvals).
- **Widget, Live Activity, Siri** (iOS; `src/widgets/`, `app-intents/`): a home screen widget (small and medium) with how many servers are running and the ones that aren't, written by the app whenever it loads servers (`src/lib/widget.ts`), so it says when it was last updated; a Live Activity while a server starts, restarts, or stops from the app, updated while the app is open; and Siri and Shortcuts intents ("Restart Survival in Raptor"; Start and Stop too), declared in Swift, which open the app and do it on the server's page. `expo-widgets` and the alpha `expo-app-intents`.
- **Design:** The Console ([docs/DESIGN.md](../docs/DESIGN.md)): `src/lib/theme.ts` has the web app's colours in hex, Hanken Grotesk and JetBrains Mono. The icons are the claw R on Signal Red.

## Running it

```bash
task mobile:ios       # a development build in the iOS Simulator (Xcode)
task mobile:android   # on an Android emulator (Android Studio)
task mobile:check     # typecheck and Biome
```

Against a local Panel, set where it is when starting Metro:

```bash
EXPO_PUBLIC_API_URL=http://localhost:8080 EXPO_PUBLIC_APP_URL=http://localhost:5173 pnpm start
```

On an Android emulator the host is `10.0.2.2`, not `localhost`. Sign in with an email code (`task dev:mail`).

Push needs a real device (the iOS Simulator can't get a token from APNs) and a Panel with `PANEL_APNS_*` or `PANEL_FCM_CREDENTIALS` ([docs/DEPLOY.md](../docs/DEPLOY.md#push-notifications)). Development builds register sandbox tokens, which the Panel sends to Apple's sandbox. Android needs `google-services.json` from the Firebase project (not committed); see below.

## Before the stores

- `google-services.json` for Android push: from Firebase, added to `app.json` as `android.googleServicesFile` (kept out of git, or from an EAS secret).
- Builds and submission with EAS (`eas.json`), store listings, privacy labels, and a demo account for App Review (#450).
