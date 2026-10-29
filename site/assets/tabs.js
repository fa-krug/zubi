// Zubi site: tabs, for a choice such as the quick start's "how do your devices connect". No
// external requests.
//
// <div data-tabs="Label"> holds panels, <section id="…" data-tab="Tab text">, each opening with
// an <h4 class="tab-title">. Without the script every panel shows under its heading; with it the
// panels become tabs. The panel whose id is the page's #fragment, or holds it, opens first;
// otherwise the one marked data-default. Choosing a tab puts its id in the address, so it can be
// linked to.
(function () {
  "use strict";

  function setup(group) {
    var panels = Array.prototype.slice.call(group.querySelectorAll(":scope > [data-tab]"));
    if (!panels.length) return;
    var list = document.createElement("div");
    list.className = "tab-list";
    list.setAttribute("role", "tablist");
    list.setAttribute("aria-label", group.getAttribute("data-tabs"));
    var buttons = panels.map(function (panel, i) {
      var button = document.createElement("button");
      button.type = "button";
      button.id = panel.id + "-tab";
      button.className = "tab";
      button.setAttribute("role", "tab");
      button.setAttribute("aria-controls", panel.id);
      button.textContent = panel.getAttribute("data-tab");
      panel.setAttribute("role", "tabpanel");
      panel.setAttribute("aria-labelledby", button.id);
      button.addEventListener("click", function () {
        select(i);
        history.replaceState(null, "", "#" + panel.id);
      });
      button.addEventListener("keydown", function (event) {
        var next = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: panels.length - 1 }[event.key];
        if (next === undefined) return;
        event.preventDefault();
        next = (next + panels.length) % panels.length;
        select(next);
        buttons[next].focus();
      });
      list.appendChild(button);
      return button;
    });

    function select(index) {
      panels.forEach(function (panel, i) {
        var on = i === index;
        panel.hidden = !on;
        buttons[i].setAttribute("aria-selected", on ? "true" : "false");
        buttons[i].tabIndex = on ? 0 : -1;
      });
    }

    function fromHash() {
      var id = decodeURIComponent(location.hash.slice(1));
      if (!id) return -1;
      for (var i = 0; i < panels.length; i++) {
        if (panels[i].id === id || panels[i].querySelector('[id="' + id.replace(/"/g, "") + '"]')) return i;
      }
      return -1;
    }

    group.insertBefore(list, group.firstChild);
    group.classList.add("tabs-ready");
    var start = fromHash();
    if (start < 0) {
      start = Math.max(0, panels.findIndex(function (panel) { return panel.hasAttribute("data-default"); }));
    }
    select(start);
    window.addEventListener("hashchange", function () {
      var i = fromHash();
      if (i >= 0) {
        select(i);
        group.scrollIntoView({ block: "start" });
      }
    });
  }

  function init() {
    Array.prototype.forEach.call(document.querySelectorAll("[data-tabs]"), setup);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
