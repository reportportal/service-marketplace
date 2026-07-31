const API = '/api/v1';
const MANIFEST_NAME = 'marketplace-manifest.json';

let selectedPlugin = null; // { id, name, latestVersion, ... }
let actionVersion = null;

function getCookie(name) {
  const m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
  return m ? decodeURIComponent(m[1]) : '';
}

async function api(path, options = {}) {
  const headers = { ...(options.headers || {}) };
  const xsrf = getCookie('XSRF-TOKEN');
  if (xsrf && options.method && options.method !== 'GET') {
    headers['X-XSRF-TOKEN'] = xsrf;
  }
  if (options.body && typeof options.body === 'object' && !(options.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(options.body);
  }
  const res = await fetch(API + path, { credentials: 'include', ...options, headers });
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = text; }
  }
  if (!res.ok) throw { status: res.status, data };
  return data;
}

function formatApiError(err) {
  const data = err?.data;
  if (!data) return 'Request failed';
  let msg = data.message || 'Request failed';
  if (Array.isArray(data.errors) && data.errors.length) {
    msg += '\n' + data.errors.map((e) => `- ${e.field || '?'}: ${e.message}`).join('\n');
  }
  return msg;
}

function escapeHtml(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function debounce(fn, ms) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}

async function extractMarketplaceManifest(file) {
  const buf = await file.arrayBuffer();
  const view = new DataView(buf);
  const bytes = new Uint8Array(buf);
  let offset = 0;
  while (offset + 30 <= bytes.length) {
    if (view.getUint32(offset, true) !== 0x04034b50) {
      offset += 1;
      continue;
    }
    const compression = view.getUint16(offset + 8, true);
    const compSize = view.getUint32(offset + 18, true);
    const nameLen = view.getUint16(offset + 26, true);
    const extraLen = view.getUint16(offset + 28, true);
    const nameStart = offset + 30;
    const nameEnd = nameStart + nameLen;
    if (nameEnd + extraLen > bytes.length) break;
    const fileName = new TextDecoder().decode(bytes.subarray(nameStart, nameEnd));
    const dataStart = nameEnd + extraLen;
    const dataEnd = dataStart + compSize;
    if (dataEnd > bytes.length) break;
    const base = fileName.split('/').pop();
    if (base === MANIFEST_NAME) {
      const data = bytes.subarray(dataStart, dataEnd);
      let text;
      if (compression === 0) {
        text = new TextDecoder().decode(data);
      } else if (compression === 8) {
        const stream = new Blob([data]).stream().pipeThrough(new DecompressionStream('deflate-raw'));
        text = await new Response(stream).text();
      } else {
        throw new Error(`Unsupported ZIP compression method ${compression}`);
      }
      return JSON.parse(text);
    }
    offset = dataEnd;
  }
  throw new Error(`${MANIFEST_NAME} not found in JAR`);
}

function publishMode() {
  return document.querySelector('input[name="publish-mode"]:checked')?.value || 'first';
}

function syncPublishModeUI() {
  const wrap = document.getElementById('plugin-id-wrap');
  const idInput = document.getElementById('publish-plugin-id');
  if (publishMode() === 'version') {
    wrap.classList.remove('hidden');
    idInput.required = true;
  } else {
    wrap.classList.add('hidden');
    idInput.required = false;
  }
}

function selectPublishVersion(pluginId) {
  document.querySelector('input[name="publish-mode"][value="version"]').checked = true;
  syncPublishModeUI();
  document.getElementById('publish-plugin-id').value = pluginId;
  document.getElementById('publish-form').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

async function previewManifest() {
  const errEl = document.getElementById('publish-error');
  const preview = document.getElementById('manifest-preview');
  errEl.textContent = '';
  preview.classList.add('hidden');
  const jar = document.getElementById('publish-jar').files[0];
  if (!jar) {
    errEl.textContent = 'Choose a plugin JAR first';
    return null;
  }
  try {
    const manifest = await extractMarketplaceManifest(jar);
    preview.textContent = JSON.stringify(manifest, null, 2);
    preview.classList.remove('hidden');
    if (publishMode() === 'version' && !document.getElementById('publish-plugin-id').value && manifest.id) {
      document.getElementById('publish-plugin-id').value = manifest.id;
    }
    return manifest;
  } catch (err) {
    errEl.textContent = err.message || 'Failed to read manifest';
    return null;
  }
}

function buildPublishFormData() {
  const jar = document.getElementById('publish-jar').files[0];
  if (!jar) throw new Error('Choose a plugin JAR first');
  const fd = new FormData();
  fd.append('jar', jar, jar.name);
  const changelog = document.getElementById('publish-changelog').files[0];
  if (changelog) fd.append('changelog', changelog, changelog.name);
  const shots = document.getElementById('publish-screenshots').files;
  for (let i = 0; i < Math.min(shots.length, 5); i++) {
    fd.append('screenshots', shots[i], shots[i].name);
  }
  return fd;
}

function hideActionForms() {
  document.getElementById('block-form-wrap').classList.add('hidden');
  document.getElementById('advisory-form-wrap').classList.add('hidden');
  actionVersion = null;
}

function showDetailResult(label, data) {
  const el = document.getElementById('detail-result');
  el.textContent = label + '\n' + JSON.stringify(data, null, 2);
  el.classList.remove('hidden');
}

function closePluginDetail() {
  selectedPlugin = null;
  hideActionForms();
  document.getElementById('plugin-detail').classList.add('hidden');
  document.getElementById('detail-error').textContent = '';
  document.getElementById('detail-result').classList.add('hidden');
  document.getElementById('version-list').innerHTML = '';
}

async function openPluginDetail(plugin) {
  selectedPlugin = plugin;
  hideActionForms();
  document.getElementById('detail-error').textContent = '';
  document.getElementById('detail-result').classList.add('hidden');
  document.getElementById('detail-title').textContent = plugin.name || plugin.id;
  document.getElementById('detail-sub').textContent =
    `${plugin.id} · latest ${plugin.latestVersion || '—'} · ${plugin.tier || ''} · ${plugin.access || ''}`;
  document.getElementById('plugin-detail').classList.remove('hidden');
  document.getElementById('plugin-detail').scrollIntoView({ behavior: 'smooth', block: 'start' });
  await loadVersions();
}

async function loadVersions() {
  if (!selectedPlugin) return;
  const list = document.getElementById('version-list');
  const errEl = document.getElementById('detail-error');
  list.innerHTML = '';
  try {
    const data = await api(`/plugins/${encodeURIComponent(selectedPlugin.id)}/versions`);
    const versions = data.versions || [];
    if (!versions.length) {
      list.innerHTML = '<li class="muted">No versions</li>';
      return;
    }
    for (const v of versions) {
      const li = document.createElement('li');
      li.className = 'version-row';

      const meta = document.createElement('div');
      meta.className = 'plugin-meta';
      let badges = '';
      if (v.blocked) {
        badges += ' <span class="badge badge-warn">blocked</span>';
      }
      const published = v.publishedAt ? `<br><small>${escapeHtml(v.publishedAt)}</small>` : '';
      const reason = v.blocked && v.blockReason
        ? `<br><small class="warn-text">${escapeHtml(v.blockReason)}</small>`
        : '';
      meta.innerHTML = `<strong>v${escapeHtml(v.version)}</strong>${badges}${published}${reason}`;

      const actions = document.createElement('div');
      actions.className = 'row-actions';

      if (!v.blocked) {
        const blockBtn = document.createElement('button');
        blockBtn.type = 'button';
        blockBtn.className = 'linkish';
        blockBtn.textContent = 'Block';
        blockBtn.addEventListener('click', () => openBlockForm(v.version));
        actions.appendChild(blockBtn);
      }

      const advBtn = document.createElement('button');
      advBtn.type = 'button';
      advBtn.className = 'linkish';
      advBtn.textContent = 'Advisory';
      advBtn.addEventListener('click', () => openAdvisoryForm(v.version));
      actions.appendChild(advBtn);

      li.appendChild(meta);
      li.appendChild(actions);
      list.appendChild(li);

      // Load advisory badge asynchronously when present.
      loadVersionAdvisoryHint(v.version, meta);
    }
  } catch (err) {
    errEl.textContent = formatApiError(err);
  }
}

async function loadVersionAdvisoryHint(version, metaEl) {
  try {
    const detail = await api(
      `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(version)}`,
    );
    if (detail.advisory) {
      const span = document.createElement('span');
      span.className = 'badge badge-info';
      span.textContent = `advisory: ${detail.advisory.severity}`;
      span.title = detail.advisory.text || '';
      metaEl.querySelector('strong')?.after(document.createTextNode(' '), span);
    }
  } catch {
    // ignore — list still usable without advisory hints
  }
}

function openBlockForm(version) {
  actionVersion = version;
  document.getElementById('advisory-form-wrap').classList.add('hidden');
  document.getElementById('block-target').textContent = `v${version}`;
  document.getElementById('block-reason').value = '';
  document.getElementById('block-form-wrap').classList.remove('hidden');
  document.getElementById('block-form-wrap').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function openAdvisoryForm(version) {
  actionVersion = version;
  document.getElementById('block-form-wrap').classList.add('hidden');
  document.getElementById('advisory-target').textContent = `v${version}`;
  document.getElementById('advisory-text').value = '';
  document.getElementById('advisory-severity').value = 'medium';
  document.getElementById('advisory-form-wrap').classList.remove('hidden');
  document.getElementById('advisory-form-wrap').scrollIntoView({ behavior: 'smooth', block: 'nearest' });

  // Prefill from existing advisory if any.
  api(`/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(version)}`)
    .then((detail) => {
      if (detail.advisory) {
        document.getElementById('advisory-severity').value = detail.advisory.severity || 'medium';
        document.getElementById('advisory-text').value = detail.advisory.text || '';
      }
    })
    .catch(() => {});
}

async function init() {
  const auth = await api('/auth/config');
  const loginSection = document.getElementById('login-section');
  const consoleSection = document.getElementById('console-section');
  const loginOptions = document.getElementById('login-options');
  const adminForm = document.getElementById('admin-login-form');

  if (auth.githubEnabled) {
    const a = document.createElement('a');
    a.href = API + '/auth/github/login';
    a.textContent = 'Sign in with GitHub';
    a.className = 'button';
    loginOptions.appendChild(a);
  }
  if (auth.adminLoginEnabled) {
    adminForm.classList.remove('hidden');
  }
  if (!auth.githubEnabled && !auth.adminLoginEnabled) {
    loginOptions.textContent = 'No login methods configured. Enable admin login or GitHub OAuth.';
  }

  try {
    // Catalogue is public — probe a session-protected route instead.
    await api('/licenses');
    loginSection.classList.add('hidden');
    consoleSection.classList.remove('hidden');
    document.getElementById('auth-status').textContent = 'Signed in';
    await loadPlugins();
  } catch {
    loginSection.classList.remove('hidden');
    consoleSection.classList.add('hidden');
    document.getElementById('auth-status').textContent = 'Not signed in';
  }

  adminForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('login-error');
    errEl.textContent = '';
    try {
      await api('/auth/login', {
        method: 'POST',
        body: {
          username: document.getElementById('username').value,
          password: document.getElementById('password').value,
        },
      });
      location.reload();
    } catch (err) {
      errEl.textContent = err.data?.message || 'Login failed';
    }
  });

  document.getElementById('logout-btn').addEventListener('click', async () => {
    try {
      await api('/auth/logout', { method: 'POST' });
    } catch {
      // Session may already be expired/missing; still leave the console.
    }
    location.reload();
  });

  document.querySelectorAll('nav button[data-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('nav button[data-tab]').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      document.querySelectorAll('.tab').forEach((t) => t.classList.add('hidden'));
      document.getElementById('tab-' + btn.dataset.tab).classList.remove('hidden');
      if (btn.dataset.tab === 'licenses') loadLicenses();
    });
  });

  document.getElementById('refresh-plugins').addEventListener('click', loadPlugins);
  document.getElementById('search').addEventListener('input', debounce(loadPlugins, 300));
  document.getElementById('category').addEventListener('change', loadPlugins);

  document.querySelectorAll('input[name="publish-mode"]').forEach((el) => {
    el.addEventListener('change', syncPublishModeUI);
  });
  syncPublishModeUI();

  document.getElementById('preview-manifest').addEventListener('click', () => {
    previewManifest();
  });

  document.getElementById('publish-jar').addEventListener('change', () => {
    document.getElementById('publish-error').textContent = '';
    document.getElementById('publish-result').classList.add('hidden');
    if (document.getElementById('publish-jar').files[0]) previewManifest();
  });

  document.getElementById('publish-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('publish-error');
    const resultEl = document.getElementById('publish-result');
    const submitBtn = document.getElementById('publish-submit');
    errEl.textContent = '';
    resultEl.classList.add('hidden');

    const manifest = await previewManifest();
    if (!manifest) return;

    const mode = publishMode();
    let path = '/plugins';
    if (mode === 'version') {
      const pluginId = document.getElementById('publish-plugin-id').value.trim() || manifest.id;
      if (!pluginId) {
        errEl.textContent = 'Plugin ID is required for a new version';
        return;
      }
      if (manifest.id && manifest.id !== pluginId) {
        errEl.textContent = `Manifest id "${manifest.id}" does not match plugin id "${pluginId}"`;
        return;
      }
      path = `/plugins/${encodeURIComponent(pluginId)}/versions`;
    }

    let body;
    try {
      body = buildPublishFormData();
    } catch (err) {
      errEl.textContent = err.message;
      return;
    }

    submitBtn.disabled = true;
    try {
      const res = await api(path, { method: 'POST', body });
      resultEl.textContent = 'Published:\n' + JSON.stringify(res, null, 2);
      resultEl.classList.remove('hidden');
      document.getElementById('publish-form').reset();
      syncPublishModeUI();
      document.getElementById('manifest-preview').classList.add('hidden');
      await loadPlugins();
      if (selectedPlugin && (selectedPlugin.id === res.pluginId || selectedPlugin.id === manifest.id)) {
        await openPluginDetail({ ...selectedPlugin, id: res.pluginId, latestVersion: res.version });
      }
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      submitBtn.disabled = false;
    }
  });

  document.getElementById('detail-close').addEventListener('click', closePluginDetail);
  document.getElementById('detail-new-version').addEventListener('click', () => {
    if (selectedPlugin) selectPublishVersion(selectedPlugin.id);
  });

  document.getElementById('block-cancel').addEventListener('click', hideActionForms);
  document.getElementById('advisory-cancel').addEventListener('click', hideActionForms);

  document.getElementById('block-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    try {
      const res = await api(
        `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(actionVersion)}/block`,
        { method: 'POST', body: { reason: document.getElementById('block-reason').value.trim() } },
      );
      showDetailResult('Blocked:', res);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    }
  });

  document.getElementById('advisory-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    try {
      const res = await api(
        `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(actionVersion)}/advisory`,
        {
          method: 'POST',
          body: {
            severity: document.getElementById('advisory-severity').value,
            text: document.getElementById('advisory-text').value.trim(),
          },
        },
      );
      showDetailResult('Advisory saved:', res);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    }
  });

  document.getElementById('remove-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    errEl.textContent = '';
    if (!selectedPlugin) return;
    const reason = document.getElementById('removal-reason').value.trim();
    const ok = window.confirm(
      `Remove plugin "${selectedPlugin.id}" permanently?\n\nThis hard-deletes artifacts and cannot be undone.`,
    );
    if (!ok) return;
    try {
      const res = await api(`/plugins/${encodeURIComponent(selectedPlugin.id)}`, {
        method: 'DELETE',
        body: { removalReason: reason },
      });
      showDetailResult('Removed:', res);
      document.getElementById('removal-reason').value = '';
      closePluginDetail();
      await loadPlugins();
      // Re-show tombstone result briefly above catalogue
      const pre = document.getElementById('publish-result');
      pre.textContent = 'Plugin removed:\n' + JSON.stringify(res, null, 2);
      pre.classList.remove('hidden');
    } catch (err) {
      errEl.textContent = formatApiError(err);
    }
  });

  document.getElementById('create-license-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = { customerId: document.getElementById('customer-id').value };
    const exp = document.getElementById('expires-at').value;
    if (exp) body.expiresAt = exp;
    const res = await api('/licenses', { method: 'POST', body });
    const pre = document.getElementById('license-result');
    pre.textContent = JSON.stringify(res, null, 2);
    pre.classList.remove('hidden');
    loadLicenses();
  });
}

async function loadPlugins() {
  const q = document.getElementById('search')?.value || '';
  const category = document.getElementById('category')?.value || '';
  const params = new URLSearchParams();
  if (q) params.set('q', q);
  if (category) params.set('category', category);
  const data = await api('/plugins?' + params.toString());
  const list = document.getElementById('plugin-list');
  list.innerHTML = '';
  (data.plugins || []).forEach((p) => {
    const li = document.createElement('li');
    const meta = document.createElement('div');
    meta.className = 'plugin-meta';
    meta.innerHTML = `<strong>${escapeHtml(p.name)}</strong> (${escapeHtml(p.id)}) v${escapeHtml(p.latestVersion)}<br><small>${escapeHtml(p.category)} · ${escapeHtml(p.access)} · ${escapeHtml(p.tier)}</small>`;

    const actions = document.createElement('div');
    actions.className = 'row-actions';

    const manageBtn = document.createElement('button');
    manageBtn.type = 'button';
    manageBtn.className = 'linkish';
    manageBtn.textContent = 'Manage';
    manageBtn.addEventListener('click', () => openPluginDetail(p));

    const verBtn = document.createElement('button');
    verBtn.type = 'button';
    verBtn.className = 'linkish';
    verBtn.textContent = 'New version';
    verBtn.addEventListener('click', () => selectPublishVersion(p.id));

    actions.appendChild(manageBtn);
    actions.appendChild(verBtn);
    li.appendChild(meta);
    li.appendChild(actions);
    list.appendChild(li);
  });
}

async function loadLicenses() {
  const data = await api('/licenses');
  const list = document.getElementById('license-list');
  list.innerHTML = '';
  (data.entitlements || []).forEach((e) => {
    const li = document.createElement('li');
    li.textContent = `${e.customerId} — ${e.tier} (${e.publicKeys?.length || 0} keys)`;
    list.appendChild(li);
  });
}

init();
