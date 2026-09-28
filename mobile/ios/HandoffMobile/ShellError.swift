import Foundation

// 四类可归因错误（contract §4.6 条 35）：分类只按「失败调用点 + Machine.Online」，
// 绝不解析 gomobile error 文案（跨语言面无结构化错误哨兵）。
enum ShellError: Error, Equatable {
    case pairing(String)
    case offline(String)
    case exchange(String)
    case expired(String)

    var title: String {
        switch self {
        case .pairing: return "配对失败"
        case .offline: return "机器离线"
        case .exchange: return "会话建立失败"
        case .expired: return "会话已过期"
        }
    }

    var message: String {
        switch self {
        case .pairing: return "配对串无效或版本不受支持，请检查后重试。"
        case .offline: return "该机器当前离线，请稍后重试。"
        case .exchange: return "无法获取该机器的会话，请重试。"
        case .expired: return "会话已过期，请重新进入该机器。"
        }
    }

    var canRetry: Bool { true }   // 四类都可重试
}

// 失败调用点——分类依据（不解析文案）。
enum CoreCallSite { case pair, switchMachine, sessionCookie }

enum ErrorClassifier {
    // pair → pairing；switchMachine/sessionCookie → 在线则 exchange、离线则 offline。
    static func classify(_ callSite: CoreCallSite, machine: String, online: Bool) -> ShellError {
        switch callSite {
        case .pair: return .pairing(machine)
        case .switchMachine, .sessionCookie:
            return online ? .exchange(machine) : .offline(machine)
        }
    }
}
