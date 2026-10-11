// Device display preference persistence across ephemeral loopback origins.
// Only the tree display key is eligible; cookies and pairing data are excluded.
import Foundation
import WebKit

final class WebDisplayPreferences: NSObject, WKScriptMessageHandler {
    private static let key = "handoff.tree.prefs"
    private static let channel = "handoffDisplayPreferences"
    private static let marker = "// handoff-display-preferences\n"
    private let store: UserDefaults
    private var activeOrigin: URLComponents?

    init(store: UserDefaults = .standard) { self.store = store }

    /// Install before loading the authenticated origin so React's first read
    /// already sees this device's saved choices, even when the port has changed.
    func install(on webView: WKWebView, origin: String) {
        guard let parsed = URLComponents(string: origin), parsed.scheme == "http",
              parsed.host == "127.0.0.1", let port = parsed.port, port > 0,
              parsed.user == nil, parsed.password == nil else {
            Log.shell.error("显示偏好拒绝非回环源")
            return
        }
        activeOrigin = parsed
        let saved = store.string(forKey: Self.key)
        let encoded = Data((saved ?? "").utf8).base64EncodedString()
        let source = Self.marker + """
        (() => {
          if (location.origin !== 'http://127.0.0.1:\(port)') return;
          const key = '\(Self.key)';
          const saved = '\(encoded)';
          const send = () => window.webkit.messageHandlers.\(Self.channel).postMessage(localStorage.getItem(key));
          try {
            // A same-origin reload already has newer choices. The backup is
            // only for a fresh origin; restoring it on every load undoes edits.
            if (saved && localStorage.getItem(key) === null) localStorage.setItem(key, new TextDecoder().decode(Uint8Array.from(atob(saved), c => c.charCodeAt(0))));
            const set = Storage.prototype.setItem;
            Storage.prototype.setItem = function(k, v) {
              set.call(this, k, v);
              if (this === localStorage && String(k) === key) send();
            };
            const remove = Storage.prototype.removeItem;
            Storage.prototype.removeItem = function(k) {
              remove.call(this, k);
              if (this === localStorage && String(k) === key) send();
            };
            const clear = Storage.prototype.clear;
            Storage.prototype.clear = function() {
              clear.call(this);
              if (this === localStorage) send();
            };
            send();
          } catch (error) { console.warn('ios.display_preferences.failed', {name: error.name}); }
        })();
        """
        let controller = webView.configuration.userContentController
        controller.removeScriptMessageHandler(forName: Self.channel)
        controller.add(self, name: Self.channel)
        // Preserve viewport and any other owner's scripts when replacing ours.
        let otherScripts = controller.userScripts.filter { !$0.source.hasPrefix(Self.marker) }
        controller.removeAllUserScripts()
        otherScripts.forEach(controller.addUserScript)
        controller.addUserScript(WKUserScript(source: source, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        Log.shell.info("显示偏好已装配 savedBytes=\(saved?.utf8.count ?? 0, privacy: .public)")
    }

    func userContentController(_ userContentController: WKUserContentController,
                               didReceive message: WKScriptMessage) {
        let source = message.frameInfo.securityOrigin
        guard message.name == Self.channel, message.frameInfo.isMainFrame,
              let activeOrigin, source.protocol == activeOrigin.scheme,
              source.host == activeOrigin.host, source.port == activeOrigin.port else {
            Log.shell.error("显示偏好忽略非活动主框消息")
            return
        }
        if message.body is NSNull {
            store.removeObject(forKey: Self.key)
            Log.shell.info("设备显示偏好已清除")
            return
        }
        guard let value = message.body as? String, value.utf8.count <= 65536 else {
            Log.shell.error("显示偏好拒绝非法或过长值")
            return
        }
        store.set(value, forKey: Self.key)
        Log.shell.info("设备显示偏好已保存 bytes=\(value.utf8.count, privacy: .public)")
    }
}
