import Foundation
import Hummingbird

/// The API's own documentation, without auth like /health: the spec holds nothing secret, and
/// Swagger UI's Authorize button supplies the key for the calls made from the page.
enum DocsRoutes {
    /// Pinned exactly, so the page never changes under us.
    static let swaggerUIVersion = "5.17.14"

    /// The bundled spec, byte for byte. Loaded once; it cannot change while the app runs.
    static let openAPIDocument: Data = {
        guard let url = Bundle.module.url(forResource: "openapi", withExtension: "json"),
              let data = try? Data(contentsOf: url)
        else { fatalError("openapi.json is missing from the ZubiAPI bundle") }
        return data
    }()

    // Root-relative, so it resolves against whatever host and port the browser used,
    // including the local end of an SSH tunnel.
    static let page = """
    <!doctype html>
    <html lang="en">
    <head>
      <meta charset="utf-8">
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <title>Zubi API</title>
      <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@\(swaggerUIVersion)/swagger-ui.css">
    </head>
    <body>
      <div id="swagger-ui"></div>
      <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@\(swaggerUIVersion)/swagger-ui-bundle.js"></script>
      <script>
        window.ui = SwaggerUIBundle({
          url: "/openapi.json",
          dom_id: "#swagger-ui",
          persistAuthorization: true
        });
      </script>
    </body>
    </html>
    """

    static func register(on router: Router<ZubiRequestContext>) {
        router.get("openapi.json") { _, _ -> Response in
            respond(openAPIDocument, contentType: "application/json")
        }
        router.get("docs") { _, _ -> Response in
            respond(Data(page.utf8), contentType: "text/html; charset=utf-8")
        }
    }

    private static func respond(_ data: Data, contentType: String) -> Response {
        Response(
            status: .ok,
            headers: [.contentType: contentType],
            body: ResponseBody(byteBuffer: ByteBuffer(bytes: data))
        )
    }
}
