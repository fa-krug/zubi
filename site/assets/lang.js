// Zubi site: the EN/DE toggle on the legal pages. No external requests.
(function () {
  "use strict";
  var KEY = "zubi-lang";

  function normalize(lang) {
    var short = (lang || "").toLowerCase().slice(0, 2);
    return short === "de" || short === "en" ? short : null;
  }

  function apply(lang) {
    document.documentElement.setAttribute("data-lang", lang);
    document.documentElement.setAttribute("lang", lang);
    try {
      localStorage.setItem(KEY, lang);
    } catch (e) {
      /* storage blocked: the choice lasts for this page only */
    }
  }

  var stored = null;
  try {
    stored = localStorage.getItem(KEY);
  } catch (e) {
    stored = null;
  }
  apply(normalize(stored) || normalize(navigator.language) || "en");

  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-set]");
    if (!button) return;
    var lang = normalize(button.getAttribute("data-set"));
    if (lang) apply(lang);
  });
})();
