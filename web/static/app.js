// Tooltip for any element with data-tip="Title|line|line".
(function () {
  var tip = document.getElementById("tip");
  if (!tip) return;
  var current = null;

  function show(el, x, y) {
    var parts = el.getAttribute("data-tip").split("|");
    tip.textContent = "";
    var title = document.createElement("strong");
    title.textContent = parts[0];
    tip.appendChild(title);
    for (var i = 1; i < parts.length; i++) {
      var line = document.createElement("div");
      line.textContent = parts[i];
      tip.appendChild(line);
    }
    tip.hidden = false;
    place(x, y);
  }

  function place(x, y) {
    var w = tip.offsetWidth, h = tip.offsetHeight;
    var left = Math.min(x + 14, window.innerWidth - w - 8);
    var top = y + 16 + h > window.innerHeight ? y - h - 12 : y + 16;
    tip.style.left = Math.max(8, left) + "px";
    tip.style.top = Math.max(8, top) + "px";
  }

  document.addEventListener("pointerover", function (e) {
    var el = e.target.closest("[data-tip]");
    if (el === current) return;
    current = el;
    if (el) show(el, e.clientX, e.clientY); else tip.hidden = true;
  });
  document.addEventListener("pointermove", function (e) {
    if (current) place(e.clientX, e.clientY);
  });
  document.addEventListener("focusin", function (e) {
    var el = e.target.closest("[data-tip]");
    if (!el) return;
    current = el;
    var r = el.getBoundingClientRect();
    show(el, r.left + r.width / 2, r.top + r.height / 2);
  });
  document.addEventListener("focusout", function () { current = null; tip.hidden = true; });
})();
