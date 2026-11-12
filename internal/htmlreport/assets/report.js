"use strict";
(function () {
  function apply(table) {
    var selects = document.querySelectorAll('select[data-filter-for="' + table.id + '"]');
    var rows = table.tBodies[0].rows;
    var shown = 0;
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var keep = true;
      for (var j = 0; j < selects.length; j++) {
        var want = selects[j].value;
        if (want !== "" && row.getAttribute("data-" + selects[j].getAttribute("data-key")) !== want) {
          keep = false;
          break;
        }
      }
      row.hidden = !keep;
      if (keep) {
        shown++;
      }
    }
    var count = document.querySelector('[data-count-for="' + table.id + '"]');
    if (count) {
      count.textContent = shown + " of " + rows.length + " shown";
    }
  }
  var tables = document.querySelectorAll("table[data-filterable]");
  for (var t = 0; t < tables.length; t++) {
    (function (table) {
      var selects = document.querySelectorAll('select[data-filter-for="' + table.id + '"]');
      for (var s = 0; s < selects.length; s++) {
        selects[s].addEventListener("change", function () { apply(table); });
      }
      apply(table);
    })(tables[t]);
  }
  document.body.setAttribute("data-ready", "ok");
})();
