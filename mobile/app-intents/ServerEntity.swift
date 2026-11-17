import AppIntents
internal import ExpoAppIntents
import Foundation

/// A server Siri and Shortcuts can name: the servers the signed-in person can
/// see, as the app publishes them (`setEntityCatalogAsync("server", ...)` in
/// src/lib/intents.ts). The ID is "<org>|<node>|<server>".
struct ServerEntity: AppEntity {
  static let typeDisplayRepresentation: TypeDisplayRepresentation = "Server"
  static let defaultQuery = ServerQuery()

  var id: String
  @Property(title: "Name")
  var name: String
  var node: String?

  var displayRepresentation: DisplayRepresentation {
    if let node {
      return DisplayRepresentation(title: "\(name)", subtitle: "\(node)")
    }
    return DisplayRepresentation(title: "\(name)")
  }

  init(record: AppIntentEntityRecord) {
    self.id = record.id
    self.name = record.title
    self.node = record.subtitle
  }

  func matches(_ text: String) -> Bool {
    let fold = { (s: String) in
      s.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
        .trimmingCharacters(in: .whitespacesAndNewlines)
    }
    let want = fold(text)
    let have = fold(name)
    return !want.isEmpty && (have == want || have.contains(want) || want.contains(have))
  }
}

struct ServerQuery: EntityStringQuery {
  func entities(for identifiers: [String]) async throws -> [ServerEntity] {
    return try await AppIntentEntityStore.shared.entities(ofKind: "server", matching: identifiers)
      .map(ServerEntity.init(record:))
  }

  func suggestedEntities() async throws -> [ServerEntity] {
    return try await AppIntentEntityStore.shared.entities(ofKind: "server")
      .map(ServerEntity.init(record:))
  }

  func entities(matching string: String) async throws -> [ServerEntity] {
    return try await suggestedEntities().filter { $0.matches(string) }
  }
}
