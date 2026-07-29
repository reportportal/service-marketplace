(() => {
  "use strict";

  let currentPluginId = null;

  const $ = (id) => document.getElementById(id);

  function jsonHeaders() {
    return { "Content-Type": "application/json" };
  }

  function appendTextCell(tr, text, tagName) {
    const td = document.createElement("td");
    if (tagName) {
      const el = document.createElement(tagName);
      el.textContent = text == null ? "" : String(text);
      td.appendChild(el);
    } else {
      td.textContent = text == null ? "" : String(text);
    }
    tr.appendChild(td);
    return td;
  }

  function trustBadge(tier) {
    const span = document.createElement("span");
    if (!tier || tier === "official") {
      span.className = "badge";
      span.title = "Trust tier is read-only in Phase 1/2";
      span.textContent = "Official";
    } else {
      span.className = "muted";
      span.textContent = String(tier);
    }
    return span;
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, { credentials: "same-origin", ...opts });
    const text = await res.text();
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    if (!res.ok) {
      const msg = body && body.message ? body.message : (typeof body === "string" ? body : res.statusText);
      throw new Error(msg || ("HTTP " + res.status));
    }
    return body;
  }

  function showView(name) {
    $("loginView").classList.toggle("hidden", name !== "login");
    $("pluginsView").classList.toggle("hidden", name !== "plugins");
    $("licensesView").classList.toggle("hidden", name !== "licenses");
    $("nav").classList.toggle("hidden", name === "login");
    document.querySelectorAll("#nav button[data-view]").forEach((b) => {
      b.classList.toggle("active", b.dataset.view === name);
    });
  }

  async function ensureSession() {
    // Session lives in an HttpOnly cookie — probe a protected endpoint rather than reading JS storage.
    try {
      await api("/api/v1/licenses");
    } catch (_) {
      showView("login");
      return false;
    }
    showView("plugins");
    await loadPlugins();
    return true;
  }

  $("loginForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    $("loginError").textContent = "";
    const fd = new FormData(e.target);
    try {
      await api("/api/v1/auth/login", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({
          username: fd.get("username"),
          password: fd.get("password")
        })
      });
      showView("plugins");
      await loadPlugins();
    } catch (err) {
      $("loginError").textContent = err.message;
    }
  });

  $("logoutBtn").addEventListener("click", async () => {
    try { await api("/api/v1/auth/logout", { method: "POST" }); } catch (_) {}
    showView("login");
  });

  document.querySelectorAll("#nav button[data-view]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      showView(btn.dataset.view);
      if (btn.dataset.view === "plugins") await loadPlugins();
      if (btn.dataset.view === "licenses") await loadLicenses();
    });
  });

  $("refreshPlugins").addEventListener("click", () => loadPlugins());
  $("refreshLicenses").addEventListener("click", () => loadLicenses());

  async function loadPlugins() {
    const data = await api("/api/v1/plugins");
    const rows = $("pluginRows");
    rows.replaceChildren();
    (data.plugins || []).forEach((p) => {
      const tr = document.createElement("tr");
      appendTextCell(tr, p.id, "code");
      appendTextCell(tr, p.name || "");
      appendTextCell(tr, p.latestVersion || "");
      appendTextCell(tr, p.category || "");
      appendTextCell(tr, p.access || "public");
      const trustTd = document.createElement("td");
      trustTd.appendChild(trustBadge(p.tier));
      tr.appendChild(trustTd);
      const actions = document.createElement("td");
      actions.className = "actions";
      const manage = document.createElement("button");
      manage.className = "btn";
      manage.type = "button";
      manage.textContent = "Manage";
      manage.addEventListener("click", () => openPlugin(p.id, p));
      actions.appendChild(manage);
      tr.appendChild(actions);
      rows.appendChild(tr);
    });
  }

  function openPlugin(id, meta) {
    currentPluginId = id;
    $("dialogTitle").textContent = id;
    const trust = meta.tier || "official";
    $("dialogMeta").textContent =
      `${meta.name || ""} · latest ${meta.latestVersion || "—"} · access ${meta.access || "public"} · trust ${trust} (read-only)`;
    $("dialogMsg").textContent = "";
    $("pluginDialog").showModal();
  }

  $("firstPublishForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    $("firstPublishMsg").textContent = "";
    $("firstPublishMsg").className = "ok";
    const fd = new FormData(e.target);
    const body = new FormData();
    body.append("jar", fd.get("jar"));
    if (fd.get("changelog") && fd.get("changelog").size) body.append("changelog", fd.get("changelog"));
    const shots = e.target.screenshots.files;
    for (const f of shots) body.append("screenshots", f);
    try {
      const res = await fetch("/api/v1/plugins", { method: "POST", credentials: "same-origin", body });
      const json = await res.json();
      if (!res.ok) throw new Error(json.message || res.statusText);
      $("firstPublishMsg").textContent = `Published ${json.pluginId}@${json.version} sha256=${json.sha256}`;
      e.target.reset();
      await loadPlugins();
    } catch (err) {
      $("firstPublishMsg").className = "error";
      $("firstPublishMsg").textContent = err.message;
    }
  });

  $("publishVersionBtn").addEventListener("click", async (e) => {
    e.preventDefault();
    const form = $("pluginDialogForm");
    const jar = form.jar.files[0];
    if (!jar) { $("dialogMsg").textContent = "Select a JAR"; return; }
    const body = new FormData();
    body.append("jar", jar);
    if (form.changelog.files[0]) body.append("changelog", form.changelog.files[0]);
    try {
      const res = await fetch(`/api/v1/plugins/${currentPluginId}/versions`, {
        method: "POST", credentials: "same-origin", body
      });
      const json = await res.json();
      if (!res.ok) throw new Error(json.message || res.statusText);
      $("dialogMsg").className = "ok";
      $("dialogMsg").textContent = `Published ${json.version}`;
      await loadPlugins();
    } catch (err) {
      $("dialogMsg").className = "error";
      $("dialogMsg").textContent = err.message;
    }
  });

  $("blockBtn").addEventListener("click", async (e) => {
    e.preventDefault();
    const form = $("pluginDialogForm");
    try {
      await api(`/api/v1/plugins/${currentPluginId}/versions/${form.blockVersion.value}/block`, {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({ reason: form.blockReason.value })
      });
      $("dialogMsg").className = "ok";
      $("dialogMsg").textContent = "Version blocked";
    } catch (err) {
      $("dialogMsg").className = "error";
      $("dialogMsg").textContent = err.message;
    }
  });

  $("advisoryBtn").addEventListener("click", async (e) => {
    e.preventDefault();
    const form = $("pluginDialogForm");
    try {
      await api(`/api/v1/plugins/${currentPluginId}/versions/${form.advisoryVersion.value}/advisory`, {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({ severity: form.severity.value, text: form.advisoryText.value })
      });
      $("dialogMsg").className = "ok";
      $("dialogMsg").textContent = "Advisory attached";
    } catch (err) {
      $("dialogMsg").className = "error";
      $("dialogMsg").textContent = err.message;
    }
  });

  $("removeBtn").addEventListener("click", async (e) => {
    e.preventDefault();
    const form = $("pluginDialogForm");
    const reason = form.removalReason.value;
    if (!reason) { $("dialogMsg").textContent = "removalReason is required"; return; }
    if (!confirm("This permanently deletes all version artifacts from storage. Continue?")) return;
    try {
      await api(`/api/v1/plugins/${currentPluginId}`, {
        method: "DELETE",
        headers: jsonHeaders(),
        body: JSON.stringify({ removalReason: reason })
      });
      $("pluginDialog").close();
      await loadPlugins();
    } catch (err) {
      $("dialogMsg").className = "error";
      $("dialogMsg").textContent = err.message;
    }
  });

  async function loadLicenses() {
    const data = await api("/api/v1/licenses");
    const rows = $("licenseRows");
    rows.replaceChildren();
    (data.entitlements || []).forEach((e) => {
      const keys = (e.publicKeys || e.keys || []).length;
      const tr = document.createElement("tr");
      appendTextCell(tr, e.customerId, "code");
      appendTextCell(tr, keys);
      appendTextCell(tr, e.issuedAt || e.createdAt || "");
      const actions = document.createElement("td");
      actions.className = "actions";
      const rotate = document.createElement("button");
      rotate.className = "btn";
      rotate.type = "button";
      rotate.textContent = "Rotate";
      rotate.addEventListener("click", async () => {
        try {
          const res = await api(`/api/v1/licenses/${encodeURIComponent(e.customerId)}/keys`, { method: "POST" });
          alert("New private key (copy now):\n" + (res.privateKey || res.privateKeyPem || ""));
          await loadLicenses();
        } catch (err) { alert(err.message); }
      });
      const revoke = document.createElement("button");
      revoke.className = "btn danger";
      revoke.type = "button";
      revoke.textContent = "Revoke";
      revoke.addEventListener("click", async () => {
        if (!confirm("Revoke entitlement for " + e.customerId + "?")) return;
        try {
          await api(`/api/v1/licenses/${encodeURIComponent(e.customerId)}`, { method: "DELETE" });
          await loadLicenses();
        } catch (err) { alert(err.message); }
      });
      actions.appendChild(rotate);
      actions.appendChild(revoke);
      tr.appendChild(actions);
      rows.appendChild(tr);
    });
  }

  $("createLicenseForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    $("createLicenseMsg").textContent = "";
    $("createLicenseMsg").className = "ok";
    const customerId = new FormData(e.target).get("customerId");
    try {
      const res = await api("/api/v1/licenses", {
        method: "POST",
        headers: jsonHeaders(),
        body: JSON.stringify({ customerId })
      });
      $("createLicenseMsg").className = "ok";
      $("createLicenseMsg").textContent =
        "Created. Private key (shown once):\n" + (res.privateKey || res.privateKeyPem);
      e.target.reset();
      await loadLicenses();
    } catch (err) {
      $("createLicenseMsg").className = "error";
      $("createLicenseMsg").textContent = err.message;
    }
  });

  ensureSession();
})();
