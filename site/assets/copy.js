// Zubi site: a copy button, an icon in the top-right corner, on every code block. No external
// requests.
//
// What a block shows as output, rather than something to type, is marked class="out" and left out
// of the copy. Blocks inside an aria-hidden illustration (the home page's terminal) get no button.
(function () {
  "use strict";

  var ICON_COPY =
    '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">' +
    '<rect x="5.25" y="5.25" width="8.5" height="8.5" rx="1.75" fill="none" stroke="currentColor" stroke-width="1.5"/>' +
    '<path d="M10.75 3.5v-.25A1.75 1.75 0 0 0 9 1.5H3.25A1.75 1.75 0 0 0 1.5 3.25V9a1.75 1.75 0 0 0 1.75 1.75h.25"' +
    ' fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';
  var ICON_DONE =
    '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">' +
    '<path d="M3 8.5l3.25 3.25L13 5" fill="none" stroke="currentColor" stroke-width="1.75"' +
    ' stroke-linecap="round" stroke-linejoin="round"/></svg>';

  function textOf(pre) {
    var clone = pre.cloneNode(true);
    Array.prototype.forEach.call(clone.querySelectorAll(".out"), function (node) {
      node.parentNode.removeChild(node);
    });
    return clone.textContent.replace(/\s+$/, "");
  }

  // The Clipboard API needs a secure context; a page opened from a file or plain HTTP falls back.
  function copy(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      var ok = false;
      try {
        ok = document.execCommand("copy");
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(area);
      if (ok) {
        resolve();
      } else {
        reject(new Error("copy refused"));
      }
    });
  }

  function addButton(pre) {
    if (pre.closest('[aria-hidden="true"]')) return;

    // The wrapper takes the block's place in the layout, margins and alignment included, so the
    // button sits on the block's corner wherever the page puts it.
    var style = window.getComputedStyle(pre);
    var wrap = document.createElement("div");
    wrap.className = "codeblock";
    wrap.style.margin = style.margin;
    wrap.style.alignSelf = style.alignSelf;
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    pre.style.margin = "0";

    var button = document.createElement("button");
    button.type = "button";
    button.className = "copy";
    button.title = "Copy";
    button.setAttribute("aria-label", "Copy");
    button.innerHTML = ICON_COPY;
    // Says "Copied" to screen readers; the icon says it to everyone else.
    var status = document.createElement("span");
    status.className = "copy-status";
    status.setAttribute("role", "status");

    var reset = null;
    function show(done, message) {
      button.innerHTML = done ? ICON_DONE : ICON_COPY;
      button.toggleAttribute("data-copied", done);
      button.title = done ? message : "Copy";
      status.textContent = done ? message : "";
    }
    button.addEventListener("click", function () {
      copy(textOf(pre)).then(
        function () { show(true, "Copied"); },
        function () {
          // Selected instead, so the keyboard's copy finishes the job.
          window.getSelection().selectAllChildren(pre);
          show(true, "Selected: press Ctrl-C or ⌘C");
        }
      );
      clearTimeout(reset);
      reset = setTimeout(function () { show(false); }, 1800);
    });
    wrap.appendChild(button);
    wrap.appendChild(status);
  }

  function init() {
    Array.prototype.forEach.call(document.querySelectorAll("pre"), addButton);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
