import Hummingbird

struct APIKeyMiddleware<Context: RequestContext>: RouterMiddleware {
    let apiKey: String
    let onRequest: @Sendable (String) -> Void

    func handle(
        _ request: Request,
        context: Context,
        next: (Request, Context) async throws -> Response
    ) async throws -> Response {
        let line = "\(request.method) \(request.uri.path)"
        let provided = request.headers[.authorization] ?? ""

        guard constantTimeEquals(provided, "Bearer \(apiKey)") else {
            onRequest("401 \(line)")
            throw HTTPError(.unauthorized)
        }

        let response = try await next(request, context)
        onRequest("\(response.status.code) \(line)")
        return response
    }

    private func constantTimeEquals(_ lhs: String, _ rhs: String) -> Bool {
        let a = Array(lhs.utf8)
        let b = Array(rhs.utf8)
        guard a.count == b.count else { return false }
        return zip(a, b).reduce(UInt8(0)) { $0 | ($1.0 ^ $1.1) } == 0
    }
}
