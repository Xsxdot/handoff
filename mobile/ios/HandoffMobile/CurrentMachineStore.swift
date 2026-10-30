import Foundation

// CurrentMachineStore —— device-local presentation preference for the selected machine.
// It stores only a name and remains separate from Keychain pairing credentials and Web prefs.
protocol CurrentMachineStore {
    func read() -> String?
    func write(_ name: String) throws
    func clear() throws
}

final class UserDefaultsCurrentMachineStore: CurrentMachineStore {
    private let defaults: UserDefaults
    private let key = "handoff.mobile.currentMachine"

    init(defaults: UserDefaults = .standard) { self.defaults = defaults }
    func read() -> String? { defaults.string(forKey: key) }
    func write(_ name: String) throws { defaults.set(name, forKey: key) }
    func clear() throws { defaults.removeObject(forKey: key) }
}
