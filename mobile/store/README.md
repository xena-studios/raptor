# App Store art

The phone app's App Store screenshots, product page header, and search results asset, made from real screens of the app (`raw/`) in a burgundy phone frame drawn in `page.html`, with The Console's type and colours ([docs/DESIGN.md](../../docs/DESIGN.md)).

| File (`out/`) | Size | Where it goes in App Store Connect |
| --- | --- | --- |
| `screenshot-1-alerts.png` … `screenshot-4-console.png` | 1206 × 2622 | Product Page → App Previews and Screenshots → iPhone with Dynamic Island (it scales them for other iPhones) |
| `header.png` | 3840 × 1646 | Product Page → Header and Search Results → Header |
| `search-results.png` | 3840 × 2560 | Product Page → Header and Search Results → Search Results |

## Making them again

```bash
node mobile/store/render.cjs            # every file; or a name, e.g. header
```

It uses the web app's Playwright (`web/node_modules`), the fonts from `site/node_modules`, and the logo from `mobile/src/lib/brand.ts`. If Playwright's Chromium isn't installed, `CHROME=<path to a Chromium>` points it at one.

## New screens

The screens in `raw/` are iPhone 18 Pro simulator captures (1206 × 2622) of a local Panel with fake servers, never production:

1. Run the Panel locally with `fakewings run -demo` ([docs/RELIABILITY.md](../../docs/RELIABILITY.md)): real games' names, steady states, use, players, and a console. Name the org and nodes in the database first (the current ones: Northside Gaming, `fsn-1`, `ash-1`), add an owner account, and add `crash` and `install_finished` alert rules so the inbox has something in it.
2. Build the app for the simulator pointed at that Panel (`EXPO_PUBLIC_API_URL`, `EXPO_PUBLIC_APP_URL`), sign in, and set the simulator to dark mode and a clean status bar: `xcrun simctl status_bar <udid> override --time 9:41 --wifiBars 3 --cellularBars 4 --batteryState discharging --batteryLevel 100`.
3. Capture at device resolution (3x) into `raw/` with the same names, then render.

Headlines and lines under them are in `page.html` (`shots`). Keep them short: one idea each, the second half in Signal Red.
