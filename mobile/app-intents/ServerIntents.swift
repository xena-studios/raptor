import AppIntents
internal import ExpoAppIntents

/// Starting, restarting, and stopping a server from Siri and Shortcuts. They
/// open the app (unlocked first if it's locked), which does it on the
/// server's page, the same as the buttons on an alert (src/lib/intents.ts).

struct RestartServerIntent: AppIntent {
  static let title: LocalizedStringResource = "Restart Server"
  static let description = IntentDescription("Restarts one of your servers in Raptor.")
  static let openAppWhenRun: Bool = true

  @Parameter(title: "Server", requestValueDialog: "Which server?")
  var server: ServerEntity

  static var parameterSummary: some ParameterSummary {
    Summary("Restart \(\.$server)")
  }

  @MainActor
  func perform() async throws -> some IntentResult & ProvidesDialog {
    await AppIntentDispatcher.shared.dispatch(
      name: "power",
      params: ["server": .string(server.id), "action": .string("restart")]
    )
    return .result(dialog: "Restarting \(server.name).")
  }
}

struct StartServerIntent: AppIntent {
  static let title: LocalizedStringResource = "Start Server"
  static let description = IntentDescription("Starts one of your servers in Raptor.")
  static let openAppWhenRun: Bool = true

  @Parameter(title: "Server", requestValueDialog: "Which server?")
  var server: ServerEntity

  static var parameterSummary: some ParameterSummary {
    Summary("Start \(\.$server)")
  }

  @MainActor
  func perform() async throws -> some IntentResult & ProvidesDialog {
    await AppIntentDispatcher.shared.dispatch(
      name: "power",
      params: ["server": .string(server.id), "action": .string("start")]
    )
    return .result(dialog: "Starting \(server.name).")
  }
}

struct StopServerIntent: AppIntent {
  static let title: LocalizedStringResource = "Stop Server"
  static let description = IntentDescription("Stops one of your servers in Raptor.")
  static let openAppWhenRun: Bool = true

  @Parameter(title: "Server", requestValueDialog: "Which server?")
  var server: ServerEntity

  static var parameterSummary: some ParameterSummary {
    Summary("Stop \(\.$server)")
  }

  @MainActor
  func perform() async throws -> some IntentResult & ProvidesDialog {
    await AppIntentDispatcher.shared.dispatch(
      name: "power",
      params: ["server": .string(server.id), "action": .string("stop")]
    )
    return .result(dialog: "Stopping \(server.name).")
  }
}

struct RaptorShortcuts: AppShortcutsProvider {
  static var appShortcuts: [AppShortcut] {
    AppShortcut(
      intent: RestartServerIntent(),
      phrases: [
        "Restart \(\.$server) in \(.applicationName)",
        "Restart a server in \(.applicationName)",
      ],
      shortTitle: "Restart Server",
      systemImageName: "arrow.clockwise"
    )
    AppShortcut(
      intent: StartServerIntent(),
      phrases: [
        "Start \(\.$server) in \(.applicationName)",
        "Start a server in \(.applicationName)",
      ],
      shortTitle: "Start Server",
      systemImageName: "play.fill"
    )
    AppShortcut(
      intent: StopServerIntent(),
      phrases: [
        "Stop \(\.$server) in \(.applicationName)",
        "Stop a server in \(.applicationName)",
      ],
      shortTitle: "Stop Server",
      systemImageName: "stop.fill"
    )
  }
}
