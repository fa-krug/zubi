#!/usr/bin/env python3
"""Build the Zubi website into _site/. Usage: python3 site/tools/build.py [out-dir]

Each file in site/pages/ is a page body that starts with a JSON comment, e.g.
<!-- {"title": "Docs", "description": "...", "nav": "docs"} -->
and is wrapped in the shared header and footer.
Standard library only, so the Pages workflow needs nothing installed.
"""
import hashlib
import html
import json
import pathlib
import re
import shutil
import sys

SITE = pathlib.Path(__file__).resolve().parent.parent
REPO = SITE.parent
OUT = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else REPO / "_site"

GITHUB = "https://github.com/fa-krug/zubi"
DOWNLOAD = GITHUB + "/releases/latest"

NAV = [("docs", "docs.html", "Docs"), ("github", GITHUB, "GitHub")]


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


def asset(name):
    """An asset's URL, versioned by its content: Pages lets browsers cache assets for an hour, and a
    page must never meet the previous release's script or stylesheet."""
    digest = hashlib.sha256((SITE / "assets" / name).read_bytes()).hexdigest()[:10]
    return f"assets/{name}?v={digest}"


def layout(meta, body):
    title = meta["title"]
    full_title = "Zubi" if title == "Zubi" else f"{title} – Zubi"
    lang_script = f'\n<script src="{asset("lang.js")}"></script>' if meta.get("bilingual") else ""
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
<link rel="stylesheet" href="{asset("styles.css")}">{lang_script}
<script src="{asset("copy.js")}" defer></script>
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


def main():
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)
    shutil.copytree(SITE / "assets", OUT / "assets")
    shutil.copy(SITE / "favicon.svg", OUT / "favicon.svg")
    # `curl -fsSL https://zubi.fa-krug.de/install.sh | sh` installs the CLI.
    shutil.copy(SITE / "install.sh", OUT / "install.sh")
    # Pages runs Jekyll on artifacts without this, which would drop nothing today but costs time.
    (OUT / ".nojekyll").write_text("")

    for page in sorted((SITE / "pages").glob("*.html")):
        text = page.read_text(encoding="utf-8")
        match = re.match(r"\s*<!--\s*(\{.*?\})\s*-->", text, re.S)
        if not match:
            sys.exit(f"{page.name}: missing the JSON front matter comment")
        meta = json.loads(match.group(1))
        (OUT / page.name).write_text(layout(meta, text[match.end():]), encoding="utf-8")

    print(f"built {len(list(OUT.glob('*.html')))} pages into {OUT}")


if __name__ == "__main__":
    main()
