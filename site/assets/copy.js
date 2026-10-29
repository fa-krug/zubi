// Zubi site: a Copy button on every code block. No external requests.
//
// What a block shows as output, rather than something to type, is marked class="out" and left out
// of the copy. Blocks inside an aria-hidden illustration (the home page's terminal) get no button.
(function () {
  "use strict";

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
    button.textContent = "Copy";
    button.setAttribute("aria-live", "polite");
    var reset = null;
    button.addEventListener("click", function () {
      copy(textOf(pre)).then(
        function () { show("Copied"); },
        function () {
          // Selected instead, so the keyboard's copy finishes the job.
          window.getSelection().selectAllChildren(pre);
          show("Selected");
        }
      );
    });
    function show(label) {
      button.textContent = label;
      button.setAttribute("data-copied", "");
      clearTimeout(reset);
      reset = setTimeout(function () {
        button.textContent = "Copy";
        button.removeAttribute("data-copied");
      }, 1600);
    }
    wrap.appendChild(button);
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
