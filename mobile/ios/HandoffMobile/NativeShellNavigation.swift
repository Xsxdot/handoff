import Foundation

// NativeShellNavigation —— recognizes the single Web-to-native action.
// It accepts no target machine, command, or URL variant from page content.
enum NativeShellNavigation {
    static let switchPath = "/_handoff/native/switch-machine"

    /// Match the exact active loopback origin and literal path in a main-frame link.
    static func isSwitchRequest(origin: String, target: URL, mainFrame: Bool) -> Bool {
        guard mainFrame,
              let active = URLComponents(string: origin),
              let requested = URLComponents(url: target, resolvingAgainstBaseURL: false),
              active.scheme == "http", active.host == "127.0.0.1", (active.port ?? 0) > 0,
              active.user == nil, active.password == nil,
              requested.scheme == active.scheme,
              requested.host == active.host,
              requested.port == active.port,
              requested.user == nil, requested.password == nil,
              requested.percentEncodedPath == switchPath,
              requested.query == nil, requested.fragment == nil else { return false }
        return true
    }
}
