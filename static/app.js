// Suivi de candidatures : petites interactions, sans dépendance.
(function () {
  "use strict";

  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };

  function store(key, value) {
    try {
      if (value === undefined) return localStorage.getItem(key);
      localStorage.setItem(key, value);
    } catch (e) { return null; }
  }

  function norm(s) {
    return (s || "").toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "");
  }

  function post(url, data) {
    var body = new URLSearchParams(data);
    return fetch(url, {
      method: "POST",
      body: body,
      headers: { "X-Requested-With": "fetch" },
      credentials: "same-origin"
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error(j.error || ("HTTP " + r.status));
        return j;
      });
    });
  }

  // ---------- Confirmations / envoi auto ----------

  document.addEventListener("submit", function (e) {
    var msg = e.target.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) e.preventDefault();
  });

  $$(".js-autosubmit").forEach(function (el) {
    el.addEventListener("change", function () { el.form.submit(); });
  });

  // ---------- Tableau d'accueil ----------

  var grid = $("#grid");
  if (grid) {
    var tbody = grid.tBodies[0];
    var rows = $$("tr", tbody);
    var search = $("#search");
    var hideClosed = $("#hide-closed");
    var countEl = $("#row-count");
    var filter = store("cand.filter") || "all";
    if (!$('.kpi[data-filter="' + filter + '"]')) filter = "all";
    if (hideClosed) hideClosed.checked = store("cand.hideClosed") === "1";

    rows.forEach(function (r) { r._search = norm(r.getAttribute("data-search")); });

    // Infobulle sur les cellules tronquées
    rows.forEach(function (r) {
      Array.prototype.forEach.call(r.cells, function (td) {
        if (!td.title && td.scrollWidth > td.clientWidth + 1) td.title = td.textContent.trim();
      });
    });

    function apply() {
      var q = search ? norm(search.value.trim()) : "";
      var visible = 0;
      rows.forEach(function (r) {
        var ok = true;
        if (filter !== "all") ok = r.getAttribute("data-group") === filter;
        if (ok && hideClosed && hideClosed.checked && filter === "all") ok = r.getAttribute("data-closed") !== "1";
        if (ok && q) ok = r._search.indexOf(q) !== -1;
        r.hidden = !ok;
        if (ok) visible++;
      });
      $$(".kpi").forEach(function (k) {
        k.setAttribute("aria-pressed", k.getAttribute("data-filter") === filter ? "true" : "false");
      });
      if (countEl) countEl.textContent = visible + " / " + rows.length + " ligne" + (rows.length > 1 ? "s" : "");
    }

    function recount() {
      var c = { total: rows.length };
      rows.forEach(function (r) {
        var g = r.getAttribute("data-group");
        c[g] = (c[g] || 0) + 1;
      });
      $$("[data-count]").forEach(function (el) { el.textContent = c[el.getAttribute("data-count")] || 0; });
    }

    $$(".kpi").forEach(function (k) {
      k.addEventListener("click", function () {
        var f = k.getAttribute("data-filter");
        filter = (filter === f && f !== "all") ? "all" : f;
        store("cand.filter", filter);
        apply();
      });
    });
    if (search) search.addEventListener("input", apply);
    if (hideClosed) hideClosed.addEventListener("change", function () {
      store("cand.hideClosed", hideClosed.checked ? "1" : "0");
      apply();
    });

    // Tri par colonne
    var sortCol = -1, sortDir = 1;
    $$("th", grid.tHead).forEach(function (th, idx) {
      th.addEventListener("click", function () {
        sortDir = (sortCol === idx) ? -sortDir : 1;
        sortCol = idx;
        var num = th.getAttribute("data-type") === "num";
        var sorted = rows.slice().sort(function (a, b) {
          var va = a.cells[idx].getAttribute("data-v") || "";
          var vb = b.cells[idx].getAttribute("data-v") || "";
          if (va === "" && vb !== "") return 1;   // vides toujours en bas
          if (vb === "" && va !== "") return -1;
          if (num) {
            var na = parseFloat(va.replace(",", ".")), nb = parseFloat(vb.replace(",", "."));
            if (isNaN(na)) na = Infinity;
            if (isNaN(nb)) nb = Infinity;
            return (na - nb) * sortDir;
          }
          return va.localeCompare(vb, "fr", { sensitivity: "base", numeric: true }) * sortDir;
        });
        sorted.forEach(function (r) { tbody.appendChild(r); });
        $$("th", grid.tHead).forEach(function (h) { h.removeAttribute("aria-sort"); });
        th.setAttribute("aria-sort", sortDir === 1 ? "ascending" : "descending");
      });
    });

    // Ligne cliquable -> fiche
    tbody.addEventListener("click", function (e) {
      if (e.target.closest("a, select, button, input")) return;
      var tr = e.target.closest("tr");
      if (tr && window.getSelection().toString() === "") window.location = "/applications/" + tr.getAttribute("data-id");
    });

    // Changement de statut directement dans le tableau
    $$(".status-select", tbody).forEach(function (sel) {
      sel.setAttribute("data-prev", sel.value);
      sel.addEventListener("change", function () {
        var tr = sel.closest("tr");
        sel.disabled = true;
        post("/applications/" + sel.getAttribute("data-id") + "/status", { status: sel.value })
          .then(function (res) {
            sel.className = "status-select st-" + res.status;
            sel.setAttribute("data-prev", res.status);
            tr.setAttribute("data-group", res.group);
            tr.cells[sel.closest("td").cellIndex].setAttribute("data-v", sel.selectedIndex);
            var closed = res.status === "refusee" || res.status === "abandon";
            tr.setAttribute("data-closed", closed ? "1" : "");
            recount();
            apply();
          })
          .catch(function (err) {
            sel.value = sel.getAttribute("data-prev");
            alert("Statut non enregistré : " + err.message);
          })
          .then(function () { sel.disabled = false; });
      });
    });

    apply();
  }

  // ---------- Fiche : édition en place ----------

  $$("[data-edit]").forEach(function (btn) {
    var key = btn.getAttribute("data-edit");
    var view = $('[data-view="' + key + '"]');
    var editor = $('[data-editor="' + key + '"]');
    if (!editor) return;
    btn.addEventListener("click", function () {
      if (view) view.hidden = true;
      editor.hidden = false;
      btn.hidden = true;
      var ta = $("textarea", editor) || $("input", editor);
      if (ta) { ta.focus(); }
    });
    $$('[data-cancel="' + key + '"]').forEach(function (c) {
      c.addEventListener("click", function () {
        editor.reset();
        editor.hidden = true;
        if (view) view.hidden = false;
        btn.hidden = false;
      });
    });
  });

  // Textes longs repliés
  $$("[data-collapsible]").forEach(function (el) {
    if (el.scrollHeight <= 400) return;
    el.classList.add("is-collapsed");
    var b = document.createElement("button");
    b.type = "button";
    b.className = "btn btn-small toggle-more";
    b.textContent = "Afficher tout";
    b.addEventListener("click", function () {
      var c = el.classList.toggle("is-collapsed");
      b.textContent = c ? "Afficher tout" : "Réduire";
    });
    el.parentNode.insertBefore(b, el.nextSibling);
  });

  // Copier dans le presse-papiers
  $$("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var src = $(btn.getAttribute("data-copy"));
      if (!src || !navigator.clipboard) return;
      navigator.clipboard.writeText(src.textContent).then(function () {
        var t = btn.textContent;
        btn.textContent = "Copié ✓";
        setTimeout(function () { btn.textContent = t; }, 1500);
      });
    });
  });

  // Modèles de contenu pour les nouvelles sections
  var kind = $("#ns-kind"), content = $("#ns-content");
  if (kind && content) {
    var lastTpl = "";
    var fill = function () {
      var opt = kind.options[kind.selectedIndex];
      var tpl = opt.getAttribute("data-template") || "";
      if (content.value.trim() === "" || content.value === lastTpl) {
        content.value = tpl;
        lastTpl = tpl;
      }
    };
    kind.addEventListener("change", fill);
    fill();
  }

  // ---------- Formulaire ----------

  function autogrow(ta) {
    var fit = function () {
      ta.style.height = "auto";
      ta.style.height = (ta.scrollHeight + 2) + "px";
    };
    ta.addEventListener("input", fit);
    fit();
  }
  $$("textarea.autogrow").forEach(autogrow);

  // Compteurs mots / caractères
  $$(".counter[data-for]").forEach(function (c) {
    var ta = document.getElementById(c.getAttribute("data-for"));
    if (!ta) return;
    var upd = function () {
      var t = ta.value.trim();
      var w = t ? t.split(/\s+/).length : 0;
      c.textContent = w + " mots · " + ta.value.length + " caractères";
    };
    ta.addEventListener("input", upd);
    upd();
  });

  var applied = $("#f-applied"), status = $("#f-status");

  function todayISO() {
    var d = new Date();
    d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
    return d.toISOString().slice(0, 10);
  }

  // Statut « envoyée » choisi sans date : date du jour.
  if (status && applied) {
    status.addEventListener("change", function () {
      if (status.value === "envoyee" && !applied.value) applied.value = todayISO();
    });
  }

  // Remplir depuis le lien
  var fb = $("#fetch-btn");
  if (fb) {
    var msg = $("#fetch-msg");
    fb.addEventListener("click", function () {
      var url = $("#f-url").value.trim();
      if (!url) { msg.textContent = "Colle d'abord le lien de l'offre."; return; }
      fb.disabled = true;
      msg.textContent = "Récupération de la page…";
      post("/fetch-offer", { url: url })
        .then(function (res) {
          var filled = [];
          var set = function (id, v, label) {
            var el = document.getElementById(id);
            if (el && v && !el.value.trim()) { el.value = v; filled.push(label); }
          };
          set("f-title", res.title, "poste");
          set("f-company", res.company, "entreprise");
          set("f-location", res.location, "lieu");
          set("f-offer", res.text, "texte de l'offre");
          var parts = [];
          if (filled.length) parts.push("Rempli : " + filled.join(", ") + ".");
          if (res.pdf) parts.push("C'est un PDF : enregistre la candidature puis « Archiver depuis le lien » pour le garder dans les fichiers.");
          if (res.warning) parts.push(res.warning);
          if (!parts.length) parts.push("Rien de nouveau à remplir (champs déjà renseignés).");
          msg.textContent = parts.join(" ");
          var ex = $("#existing-alert");
          if (ex && res.existing_id) {
            $("#existing-link").href = "/applications/" + res.existing_id;
            ex.hidden = false;
          }
        })
        .catch(function (err) { msg.textContent = err.message; })
        .then(function () { fb.disabled = false; });
    });
  }

  // ---------- Import ----------

  $$("[data-confirm-replace]").forEach(function (btn) {
    btn.form.addEventListener("submit", function (e) {
      var mode = btn.form.querySelector('input[name="mode"]:checked');
      if (mode && mode.value === "replace" &&
          !confirm("Tout remplacer : toutes les candidatures actuelles seront supprimées puis remplacées par le contenu du fichier. Continuer ?")) {
        e.preventDefault();
      }
    });
  });
})();
