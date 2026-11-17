internal import ExpoAppIntents
internal import ExpoModulesCore

/// Registered Expo inline module that wires app-target App Intents code to expo-app-intents.
/// Do not change the name of this class.
final class AppIntentsSetup: Module {
  public func definition() -> ExpoModulesCore.ModuleDefinition {
    Name("AppIntentsSetup")

    OnCreate {
      // Siri's phrases name servers from the catalog the app publishes; refresh
      // them whenever it does.
      Task {
        await AppIntentDispatcher.shared.setShortcutsRefreshHandler {
          RaptorShortcuts.updateAppShortcutParameters()
        }
        RaptorShortcuts.updateAppShortcutParameters()
      }
    }
  }
}
