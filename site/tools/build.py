#!/usr/bin/env python3
"""Build the Zubi website into _site/. Usage: python3 site/tools/build.py [out-dir]

Each file in site/pages/ is a page body that starts with a JSON comment, e.g.
<!-- {"title": "Docs", "description": "...", "nav": "docs"} -->
and is wrapped in the shared header and footer. api.html is generated from the API's
openapi.json, so the published reference is always the one the app serves.
Standard library only, so the Pages workflow needs nothing installed.
"""
import html
import json
import pathlib
import re
import shutil
import sys

SITE = pathlib.Path(__file__).resolve().parent.parent
REPO = SITE.parent
SPEC = REPO / "Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json"
OUT = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else REPO / "_site"

GITHUB = "https://github.com/fa-krug/zubi"
DOWNLOAD = GITHUB + "/releases/latest"

NAV = [("docs", "docs.html", "Docs"), ("api", "api.html", "API"), ("github", GITHUB, "GitHub")]


def header(active):
    links = []
    for key, href, label in NAV:
        current = ' aria-current="page"' if key == active else ""
        keep = ' class="nav-keep"' if key == "docs" else ""
        links.append(f'<a href="{href}"{current}{keep}>{label}</a>')
    links.append(f'<a class="button button-primary" href="{DOWNLOAD}">Download</a>')
    return f"""<a class="skip" href="#main">Skip to content</a>
<header class="site-header">
  <div class="wrap">
    <a class="brand" href="./"><img src="assets/logo.svg" alt="" width="30" height="30">Zubi</a>
    <nav class="site-nav" aria-label="Main">{"".join(links)}</nav>
  </div>
</header>"""


FOOTER = f"""<footer class="site-footer">
  <div class="wrap">
    <span class="copyright">© 2026 Sascha Krug. Zubi is free and open source under the MIT License.</span>
    <a href="imprint.html">Imprint</a>
    <a href="privacy.html">Privacy</a>
    <a href="terms.html">Terms</a>
    <a href="{GITHUB}/blob/main/LICENSE">License</a>
    <a href="{GITHUB}">GitHub</a>
  </div>
</footer>"""


def layout(meta, body):
    title = meta["title"]
    full_title = "Zubi" if title == "Zubi" else f"{title} – Zubi"
    lang_script = '\n<script src="assets/lang.js"></script>' if meta.get("bilingual") else ""
    # The 404 page is served at whatever path was missing, so it pins its relative links to the site root.
    base = f'\n<base href="{meta["base"]}">' if meta.get("base") else ""
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">{base}
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(full_title)}</title>
<meta name="description" content="{html.escape(meta["description"])}">
<meta name="color-scheme" content="light dark">
<meta property="og:title" content="{html.escape(full_title)}">
<meta property="og:description" content="{html.escape(meta["description"])}">
<link rel="icon" href="favicon.svg" type="image/svg+xml">
<link rel="stylesheet" href="assets/styles.css">{lang_script}
</head>
<body>
{header(meta.get("nav"))}
<main id="main">
{body.strip()}
</main>
{FOOTER}
</body>
</html>
"""


# --- API reference -------------------------------------------------------------------------------

def inline(text):
    """The spec's descriptions use `code` spans and nothing else."""
    return re.sub(r"`([^`]+)`", r"<code>\1</code>", html.escape(text or ""))


def slug(text):
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")


def type_of(schema, spec):
    if "$ref" in schema:
        name = schema["$ref"].rsplit("/", 1)[-1]
        return f'<a href="#schema-{slug(name)}">{name}</a>'
    kind = schema.get("type", "")
    # OpenAPI 3.1 spells "may be null" as a type list, e.g. ["string", "null"].
    if isinstance(kind, list):
        kind = " or ".join("null" if k == "null" else k for k in kind)
    if kind == "array":
        return f"array of {type_of(schema['items'], spec)}"
    detail = []
    if "enum" in schema:
        detail.append(" or ".join(f"<code>{html.escape(str(v))}</code>" for v in schema["enum"]))
    if "format" in schema:
        detail.append(schema["format"])
    if "minimum" in schema and "maximum" in schema:
        detail.append(f"{schema['minimum']}–{schema['maximum']}")
    if "default" in schema:
        detail.append(f"default {schema['default']}")
    return kind + (f" ({', '.join(detail)})" if detail else "")


def api_page(spec):
    info = spec["info"]
    ops, toc = [], []
    for path, methods in spec["paths"].items():
        for method, op in methods.items():
            anchor = slug(f"{method} {path}")
            secured = op.get("security", spec.get("security")) != []
            toc.append(f'<li><a href="#{anchor}">{method.upper()} {html.escape(path)}</a></li>')
            parts = [f'<section class="op" id="{anchor}">',
                     '<div class="op-head">',
                     f'<span class="verb">{method.upper()}</span>',
                     f'<span class="op-path">{html.escape(path)}</span>',
                     f'<span class="op-auth">{"API key" if secured else "No key needed"}</span>',
                     "</div>",
                     f"<h3>{inline(op.get('summary'))}</h3>"]
            if op.get("description"):
                parts.append(f"<p>{inline(op['description'])}</p>")
            params = op.get("parameters", [])
            if params:
                rows = "".join(
                    f"<tr><td><code>{html.escape(p['name'])}</code>"
                    f"{'' if p.get('required') else ' <span class=optional>optional</span>'}</td>"
                    f"<td>{p['in']}</td>"
                    f"<td>{type_of(p['schema'], spec)}</td><td>{inline(p.get('description'))}"
                    f"{'<br><span class=optional>e.g. <code>' + html.escape(str(p['example'])) + '</code></span>' if 'example' in p else ''}</td></tr>"
                    for p in params)
                parts.append('<div class="table-scroll"><table class="schema"><thead><tr><th>Parameter</th><th>In</th>'
                             f"<th>Type</th><th>Meaning</th></tr></thead><tbody>{rows}</tbody></table></div>")
            request = op.get("requestBody")
            if request:
                for media, content in request["content"].items():
                    parts.append(f"<p>Request body: {type_of(content['schema'], spec)} "
                                 f"<span class=optional>{media}</span>. {inline(request.get('description'))}</p>")
                    if "example" in content:
                        example = json.dumps(content["example"], indent=2, ensure_ascii=False)
                        parts.append(f"<pre>{html.escape(example)}</pre>")
            rows = []
            for code, response in op["responses"].items():
                body = ""
                for media, content in response.get("content", {}).items():
                    body = f"{type_of(content['schema'], spec)} <span class=optional>{media}</span>"
                rows.append(f"<tr><td><code>{code}</code></td><td>{inline(response['description'])}</td><td>{body}</td></tr>")
            parts.append('<div class="table-scroll"><table class="schema"><thead><tr><th>Status</th><th>When</th>'
                         f"<th>Body</th></tr></thead><tbody>{''.join(rows)}</tbody></table></div>")
            parts.append("</section>")
            ops.append("\n".join(parts))

    schemas, schema_toc = [], []
    for name, schema in spec["components"]["schemas"].items():
        schema_toc.append(f'<li><a href="#schema-{slug(name)}">{name}</a></li>')
        required = set(schema.get("required", []))
        rows = "".join(
            f"<tr><td>{html.escape(prop)}{'' if prop in required else ' <span class=optional>optional</span>'}</td>"
            f"<td>{type_of(sub, spec)}</td><td>{inline(sub.get('description'))}</td></tr>"
            for prop, sub in schema.get("properties", {}).items())
        schemas.append(f'<section class="op" id="schema-{slug(name)}"><h3>{name}</h3>'
                       f'{"<p>" + inline(schema["description"]) + "</p>" if schema.get("description") else ""}'
                       '<div class="table-scroll"><table class="schema"><thead><tr><th>Field</th><th>Type</th>'
                       f"<th>Meaning</th></tr></thead><tbody>{rows}</tbody></table></div></section>")

    description = "".join(f"<p>{inline(p)}</p>" for p in info["description"].split("\n\n"))
    return f"""<div class="wrap page">
<nav class="toc" aria-label="On this page">
  <p>Endpoints</p>
  <ol>{"".join(toc)}</ol>
  <p style="margin-top:22px">Schemas</p>
  <ol>{"".join(schema_toc)}</ol>
</nav>
<article class="prose">
<h1>API reference</h1>
<p class="lede">Every endpoint a Mac running Zubi serves, generated from the same
<a href="openapi.json">openapi.json</a> the app ships, so it can't fall behind the app.</p>
{description}
<p class="note">Optional fields are left out of a response when they have no value; they are never
<code>null</code>. Errors answer <code>{{"error": {{"message": "…"}}}}</code>, except 401, whose body is empty.
On your own Mac, <code>http://127.0.0.1:8787/docs</code> shows this API in Swagger UI, where you can try
each call with your key.</p>
<h2 id="endpoints">Endpoints</h2>
{"".join(ops)}
<h2 id="schemas">Schemas</h2>
{"".join(schemas)}
</article>
</div>"""


def main():
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)
    shutil.copytree(SITE / "assets", OUT / "assets")
    shutil.copy(SITE / "favicon.svg", OUT / "favicon.svg")
    # `curl -fsSL https://fa-krug.github.io/zubi/install.sh | sh` installs the CLI.
    shutil.copy(SITE / "install.sh", OUT / "install.sh")
    shutil.copy(SPEC, OUT / "openapi.json")
    # Pages runs Jekyll on artifacts without this, which would drop nothing today but costs time.
    (OUT / ".nojekyll").write_text("")

    for page in sorted((SITE / "pages").glob("*.html")):
        text = page.read_text(encoding="utf-8")
        match = re.match(r"\s*<!--\s*(\{.*?\})\s*-->", text, re.S)
        if not match:
            sys.exit(f"{page.name}: missing the JSON front matter comment")
        meta = json.loads(match.group(1))
        (OUT / page.name).write_text(layout(meta, text[match.end():]), encoding="utf-8")

    spec = json.loads(SPEC.read_text(encoding="utf-8"))
    meta = {"title": "API reference", "nav": "api",
            "description": "Every endpoint of Zubi's HTTP API, with parameters, responses and schemas."}
    (OUT / "api.html").write_text(layout(meta, api_page(spec)), encoding="utf-8")
    print(f"built {len(list(OUT.glob('*.html')))} pages into {OUT}")


if __name__ == "__main__":
    main()
