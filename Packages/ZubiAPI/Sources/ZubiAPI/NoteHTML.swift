/// The body HTML Notes gets for a note written through the API. Notes takes a note's title from the
/// first line of its body, so the title always comes first, then the body the request sent.
public enum NoteHTML {
    public static func body(title: String, _ body: NoteBody?) -> String {
        let heading = "<div><h1>\(escape(title))</h1></div>"
        switch body {
        case nil: return heading
        case .html(let html): return heading + html
        case .text(let text): return heading + paragraphs(text)
        }
    }

    /// One `<div>` per line, as Notes writes them, so line breaks survive. `isNewline` also splits
    /// on "\r\n", which Swift counts as a single character.
    static func paragraphs(_ text: String) -> String {
        text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
            .map { $0.isEmpty ? "<div><br></div>" : "<div>\(escape($0))</div>" }
            .joined()
    }

    static func escape(_ text: some StringProtocol) -> String {
        var escaped = ""
        for character in text {
            switch character {
            case "&": escaped += "&amp;"
            case "<": escaped += "&lt;"
            case ">": escaped += "&gt;"
            case "\"": escaped += "&quot;"
            case "'": escaped += "&#39;"
            default: escaped.append(character)
            }
        }
        return escaped
    }
}
