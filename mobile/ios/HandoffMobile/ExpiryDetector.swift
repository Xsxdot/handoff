import Foundation

// 会话过期检测（I5 条 34）：只按 HTTP 状态码判 401，不解析正文（与条 35 一致）。
enum ExpiryDetector {
    static func isExpired(httpStatusCode: Int) -> Bool { httpStatusCode == 401 }
}
