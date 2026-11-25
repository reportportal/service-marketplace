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

function setAuthStatus(signedIn) {
  const el = document.getElementById('auth-status');
  el.textContent = signedIn ? 'Signed in' : 'Not signed in';
  el.classList.toggle('is-signed-in', signedIn);
}

function showBox(id, open = true) {
  const el = document.getElementById(id);
  if (!el) return;
  el.classList.remove('hidden');
  if (el.tagName === 'DETAILS') el.open = open;
}

function hideBox(id) {
  const el = document.getElementById(id);
  if (!el) return;
  el.classList.add('hidden');
  if (el.tagName === 'DETAILS') el.open = false;
}

function setAlert(id, message, visible = true) {
  const el = document.getElementById(id);
  if (!el) return;
  el.textContent = message || '';
  el.classList.toggle('hidden', !visible || !message);
}

function setBusy(btn, busy, busyLabel, idleLabel) {
  if (!btn) return;
  btn.disabled = busy;
  btn.textContent = busy ? busyLabel : idleLabel;
}

function syncFileName(inputId) {
  const input = document.getElementById(inputId);
  const label = document.querySelector(`.field-file-name[data-for="${inputId}"]`);
  if (!input || !label) return;
  const files = Array.from(input.files || []);
  label.textContent = files.length
    ? files.map((f) => f.name).join(', ')
    : '';
}

function wireFileNameHints(ids) {
  ids.forEach((id) => {
    const input = document.getElementById(id);
    if (!input) return;
    input.addEventListener('change', () => syncFileName(id));
    syncFileName(id);
  });
}

function badgeHtml(text, kind = 'neutral') {
  return ` <span class="badge badge-${kind}">${escapeHtml(text)}</span>`;
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
  hideBox('manifest-preview-box');
  const jar = document.getElementById('publish-jar').files[0];
  if (!jar) {
    errEl.textContent = 'Choose a plugin JAR first';
    return null;
  }
  try {
    const manifest = await extractMarketplaceManifest(jar);
    preview.textContent = JSON.stringify(manifest, null, 2);
    showBox('manifest-preview-box', true);
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
  document.getElementById('ceiling-form-wrap').classList.add('hidden');
  actionVersion = null;
}

function showDetailResult(label, data) {
  setAlert('detail-success', label, true);
  if (data == null) {
    hideBox('detail-result-box');
    return;
  }
  const el = document.getElementById('detail-result');
  el.textContent = JSON.stringify(data, null, 2);
  showBox('detail-result-box', false);
}

function closePluginDetail() {
  selectedPlugin = null;
  hideActionForms();
  document.getElementById('plugin-detail').classList.add('hidden');
  document.getElementById('detail-error').textContent = '';
  setAlert('detail-success', '', false);
  hideBox('detail-result-box');
  document.getElementById('version-list').innerHTML = '';
  document.getElementById('version-empty').classList.add('hidden');
}

async function openPluginDetail(plugin) {
  selectedPlugin = plugin;
  hideActionForms();
  resetRemoveForm();
  document.getElementById('detail-error').textContent = '';
  setAlert('detail-success', '', false);
  hideBox('detail-result-box');
  document.getElementById('detail-title').textContent = plugin.name || plugin.id;
  const bits = [
    plugin.id,
    plugin.latestVersion ? `latest ${plugin.latestVersion}` : null,
  ].filter(Boolean);
  document.getElementById('detail-sub').innerHTML =
    escapeHtml(bits.join(' · ')) +
    (plugin.tier ? badgeHtml(plugin.tier, 'info') : '') +
    (plugin.access ? badgeHtml(plugin.access, 'neutral') : '');
  document.getElementById('plugin-detail').classList.remove('hidden');
  document.getElementById('plugin-detail').scrollIntoView({ behavior: 'smooth', block: 'start' });
  await loadVersions();
}

async function loadVersions() {
  if (!selectedPlugin) return;
  const list = document.getElementById('version-list');
  const empty = document.getElementById('version-empty');
  const errEl = document.getElementById('detail-error');
  list.innerHTML = '';
  empty.classList.add('hidden');
  try {
    const data = await api(`/plugins/${encodeURIComponent(selectedPlugin.id)}/versions`);
    const versions = data.versions || [];
    if (!versions.length) {
      empty.classList.remove('hidden');
      list.classList.add('hidden');
      return;
    }
    list.classList.remove('hidden');
    for (const v of versions) {
      const li = document.createElement('li');
      li.className = 'version-row';

      const meta = document.createElement('div');
      meta.className = 'plugin-meta';
      let badges = '';
      if (v.blocked) {
        badges += badgeHtml('blocked', 'warn');
      }
      if (v.compatibilityCeiling) {
        badges += badgeHtml('ceiling', 'warn');
      }
      const published = v.publishedAt ? `<br><small>${escapeHtml(v.publishedAt)}</small>` : '';
      const declared = v.compatibility && v.compatibility.reportportal;
      const effective = v.effectiveCompatibility && v.effectiveCompatibility.reportportal;
      let range = '';
      if (declared) {
        range = `<br><small>${escapeHtml(declared)}</small>`;
        if (effective && effective !== declared) {
          range += `<br><small>effective ${escapeHtml(effective)}</small>`;
        }
      }
      const reason = v.blocked && v.blockReason
        ? `<br><small class="warn-text">${escapeHtml(v.blockReason)}</small>`
        : '';
      const ceiling = v.compatibilityCeilingReason
        ? `<br><small class="warn-text">ceiling: ${escapeHtml(v.compatibilityCeilingReason)}</small>`
        : '';
      meta.innerHTML = `<strong>v${escapeHtml(v.version)}</strong>${badges}${published}${range}${reason}${ceiling}`;

      const actions = document.createElement('div');
      actions.className = 'row-actions';

      if (!v.blocked) {
        const blockBtn = document.createElement('button');
        blockBtn.type = 'button';
        blockBtn.className = 'btn btn-ghost';
        blockBtn.textContent = 'Block';
        blockBtn.addEventListener('click', () => openBlockForm(v.version));
        actions.appendChild(blockBtn);
      }

      const advBtn = document.createElement('button');
      advBtn.type = 'button';
      advBtn.className = 'btn btn-ghost';
      advBtn.textContent = 'Advisory';
      advBtn.addEventListener('click', () => openAdvisoryForm(v.version));
      actions.appendChild(advBtn);

      const ceilingBtn = document.createElement('button');
      ceilingBtn.type = 'button';
      ceilingBtn.className = 'btn btn-ghost';
      ceilingBtn.textContent = 'Ceiling';
      ceilingBtn.addEventListener('click', () => openCeilingForm(
        v.version,
        v.compatibilityCeiling || '',
        v.compatibilityCeilingReason || '',
      ));
      actions.appendChild(ceilingBtn);

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
      const sev = detail.advisory.severity || 'info';
      const kind = sev === 'critical' || sev === 'high' ? 'danger' : sev === 'medium' ? 'warn' : 'info';
      span.className = `badge badge-${kind}`;
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
  document.getElementById('ceiling-form-wrap').classList.add('hidden');
  document.getElementById('block-target').textContent = `v${version}`;
  document.getElementById('block-reason').value = '';
  document.getElementById('block-form-wrap').classList.remove('hidden');
  document.getElementById('block-form-wrap').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function openCeilingForm(version, bound, reason) {
  actionVersion = version;
  document.getElementById('block-form-wrap').classList.add('hidden');
  document.getElementById('advisory-form-wrap').classList.add('hidden');
  document.getElementById('ceiling-target').textContent = `v${version}`;
  document.getElementById('ceiling-bound').value = bound;
  document.getElementById('ceiling-reason').value = reason;
  document.getElementById('ceiling-clear').classList.toggle('hidden', !bound);
  document.getElementById('ceiling-form-wrap').classList.remove('hidden');
  document.getElementById('ceiling-form-wrap').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function openAdvisoryForm(version) {
  actionVersion = version;
  document.getElementById('block-form-wrap').classList.add('hidden');
  document.getElementById('ceiling-form-wrap').classList.add('hidden');
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

function setActiveTab(tabName) {
  document.querySelectorAll('.console-nav .tab-btn').forEach((b) => {
    const active = b.dataset.tab === tabName;
    b.classList.toggle('active', active);
    b.setAttribute('aria-selected', active ? 'true' : 'false');
  });
  document.querySelectorAll('.tab').forEach((t) => t.classList.add('hidden'));
  document.getElementById('tab-' + tabName).classList.remove('hidden');
  if (tabName === 'licenses') loadLicenses();
  if (tabName === 'removed') {
    document.getElementById('removed-error').textContent = '';
    setAlert('removed-success', '', false);
    loadRemoved();
  }
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
    a.className = 'btn btn-primary btn-block';
    loginOptions.appendChild(a);
  }
  if (auth.adminLoginEnabled) {
    adminForm.classList.remove('hidden');
    if (auth.githubEnabled) {
      document.getElementById('admin-login-divider').classList.remove('hidden');
    }
  }
  if (!auth.githubEnabled && !auth.adminLoginEnabled) {
    loginOptions.textContent = 'No login methods configured. Enable admin login or GitHub OAuth.';
  }

  try {
    // Catalogue is public — probe a session-protected route instead.
    await api('/licenses');
    loginSection.classList.add('hidden');
    consoleSection.classList.remove('hidden');
    setAuthStatus(true);
    await loadPlugins();
  } catch {
    loginSection.classList.remove('hidden');
    consoleSection.classList.add('hidden');
    setAuthStatus(false);
  }

  adminForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('login-error');
    const submitBtn = adminForm.querySelector('button[type="submit"]');
    errEl.textContent = '';
    setBusy(submitBtn, true, 'Signing in…', 'Sign in');
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
      setBusy(submitBtn, false, 'Signing in…', 'Sign in');
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

  document.querySelectorAll('.console-nav .tab-btn').forEach((btn) => {
    btn.addEventListener('click', () => setActiveTab(btn.dataset.tab));
  });

  document.getElementById('refresh-plugins').addEventListener('click', loadPlugins);
  document.getElementById('search').addEventListener('input', debounce(loadPlugins, 300));
  document.getElementById('category').addEventListener('change', loadPlugins);

  document.querySelectorAll('input[name="publish-mode"]').forEach((el) => {
    el.addEventListener('change', syncPublishModeUI);
  });
  syncPublishModeUI();
  wireFileNameHints(['publish-jar', 'publish-changelog', 'publish-screenshots']);

  document.getElementById('preview-manifest').addEventListener('click', () => {
    previewManifest();
  });

  document.getElementById('publish-jar').addEventListener('change', () => {
    document.getElementById('publish-error').textContent = '';
    setAlert('publish-success', '', false);
    hideBox('publish-result-box');
    if (document.getElementById('publish-jar').files[0]) previewManifest();
  });

  document.getElementById('publish-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('publish-error');
    const resultEl = document.getElementById('publish-result');
    const submitBtn = document.getElementById('publish-submit');
    errEl.textContent = '';
    setAlert('publish-success', '', false);
    hideBox('publish-result-box');

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

    setBusy(submitBtn, true, 'Publishing…', 'Publish');
    try {
      const res = await api(path, { method: 'POST', body });
      setAlert('publish-success', `Published ${res.pluginId || manifest.id} v${res.version || ''}`.trim(), true);
      resultEl.textContent = JSON.stringify(res, null, 2);
      showBox('publish-result-box', false);
      document.getElementById('publish-form').reset();
      syncPublishModeUI();
      wireFileNameHints(['publish-jar', 'publish-changelog', 'publish-screenshots']);
      hideBox('manifest-preview-box');
      await loadPlugins();
      if (selectedPlugin && (selectedPlugin.id === res.pluginId || selectedPlugin.id === manifest.id)) {
        await openPluginDetail({ ...selectedPlugin, id: res.pluginId, latestVersion: res.version });
      }
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, 'Publishing…', 'Publish');
    }
  });

  document.getElementById('detail-close').addEventListener('click', closePluginDetail);
  document.getElementById('detail-new-version').addEventListener('click', () => {
    if (selectedPlugin) selectPublishVersion(selectedPlugin.id);
  });

  document.getElementById('block-cancel').addEventListener('click', hideActionForms);
  document.getElementById('advisory-cancel').addEventListener('click', hideActionForms);
  document.getElementById('ceiling-cancel').addEventListener('click', hideActionForms);

  document.getElementById('block-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    const submitBtn = e.target.querySelector('button[type="submit"]');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    setBusy(submitBtn, true, 'Blocking…', 'Block version');
    try {
      const res = await api(
        `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(actionVersion)}/block`,
        { method: 'POST', body: { reason: document.getElementById('block-reason').value.trim() } },
      );
      showDetailResult('Version blocked', res);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, 'Blocking…', 'Block version');
    }
  });

  document.getElementById('ceiling-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    const submitBtn = e.target.querySelector('button[type="submit"]');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    setBusy(submitBtn, true, 'Saving…', 'Save ceiling');
    try {
      const res = await api(
        `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(actionVersion)}/compatibility-ceiling`,
        {
          method: 'PUT',
          body: {
            reportportal: document.getElementById('ceiling-bound').value.trim(),
            reason: document.getElementById('ceiling-reason').value.trim(),
          },
        },
      );
      showDetailResult('Compatibility ceiling saved', res);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, 'Saving…', 'Save ceiling');
    }
  });

  document.getElementById('ceiling-clear').addEventListener('click', async () => {
    const errEl = document.getElementById('detail-error');
    const btn = document.getElementById('ceiling-clear');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    setBusy(btn, true, 'Clearing…', 'Clear ceiling');
    try {
      await api(
        `/plugins/${encodeURIComponent(selectedPlugin.id)}/versions/${encodeURIComponent(actionVersion)}/compatibility-ceiling`,
        { method: 'DELETE' },
      );
      showDetailResult('Compatibility ceiling cleared', null);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(btn, false, 'Clearing…', 'Clear ceiling');
    }
  });

  document.getElementById('advisory-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    const submitBtn = e.target.querySelector('button[type="submit"]');
    errEl.textContent = '';
    if (!selectedPlugin || !actionVersion) return;
    setBusy(submitBtn, true, 'Saving…', 'Save advisory');
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
      showDetailResult('Advisory saved', res);
      hideActionForms();
      await loadVersions();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, 'Saving…', 'Save advisory');
    }
  });

  document.querySelectorAll('input[name="remove-mode"]').forEach((r) => r.addEventListener('change', syncRemoveMode));
  syncRemoveMode();

  document.getElementById('remove-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = document.getElementById('detail-error');
    const submitBtn = e.target.querySelector('button[type="submit"]');
    errEl.textContent = '';
    if (!selectedPlugin) return;
    const erase = removeMode() === 'erase';
    const idleLabel = erase ? 'Erase plugin' : 'Remove plugin';
    const reason = document.getElementById('removal-reason').value.trim();
    const removedId = selectedPlugin.id;

    if (erase) {
      if (!confirmErase(removedId)) {
        errEl.textContent = 'The plugin id did not match, so nothing was erased.';
        return;
      }
    } else if (
      !window.confirm(
        `Remove plugin "${removedId}" permanently?\n\nThis hard-deletes every version and cannot be undone. ` +
          'The id stays closed until you restore it.',
      )
    ) {
      return;
    }

    setBusy(submitBtn, true, erase ? 'Erasing…' : 'Removing…', idleLabel);
    try {
      const body = erase ? { leaveTombstone: false } : { removalReason: reason };
      if (erase && reason) body.removalReason = reason;
      const res = await api(`/plugins/${encodeURIComponent(removedId)}`, { method: 'DELETE', body });
      resetRemoveForm();
      closePluginDetail();
      await loadPlugins();
      setAlert(
        'publish-success',
        erase
          ? `Plugin erased: ${removedId}. The id is free; its repository can publish to it again.`
          : `Plugin removed: ${removedId}. It is listed under the Removed tab.`,
        true,
      );
      document.getElementById('publish-result').textContent = JSON.stringify(res, null, 2);
      showBox('publish-result-box', false);
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, erase ? 'Erasing…' : 'Removing…', idleLabel);
      // the form may have been reset to the other mode while the request ran; label follows it
      syncRemoveMode();
    }
  });

  document.getElementById('create-license-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const submitBtn = document.getElementById('license-submit');
    const errEl = document.getElementById('license-error');
    errEl.textContent = '';
    setAlert('license-success', '', false);
    hideBox('license-result-box');
    const body = { customerId: document.getElementById('customer-id').value };
    const exp = document.getElementById('expires-at').value;
    if (exp) body.expiresAt = exp;
    setBusy(submitBtn, true, 'Creating…', 'Create entitlement');
    try {
      const res = await api('/licenses', { method: 'POST', body });
      setAlert('license-success', `Entitlement created for ${res.customerId || body.customerId}`, true);
      document.getElementById('license-result').textContent = JSON.stringify(res, null, 2);
      showBox('license-result-box', false);
      e.target.reset();
      await loadLicenses();
    } catch (err) {
      errEl.textContent = formatApiError(err);
    } finally {
      setBusy(submitBtn, false, 'Creating…', 'Create entitlement');
    }
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
  const empty = document.getElementById('plugin-empty');
  list.innerHTML = '';
  const plugins = data.plugins || [];
  empty.classList.toggle('hidden', plugins.length > 0);
  list.classList.toggle('hidden', plugins.length === 0);

  plugins.forEach((p) => {
    const li = document.createElement('li');
    const meta = document.createElement('div');
    meta.className = 'plugin-meta';
    meta.innerHTML =
      `<strong>${escapeHtml(p.name)}</strong> <small>(${escapeHtml(p.id)})</small>` +
      ` <span class="muted">v${escapeHtml(p.latestVersion)}</span><br>` +
      `<small>${escapeHtml(p.category || '')}</small>` +
      (p.access ? badgeHtml(p.access, 'neutral') : '') +
      (p.tier ? badgeHtml(p.tier, 'info') : '');

    const actions = document.createElement('div');
    actions.className = 'row-actions';

    const manageBtn = document.createElement('button');
    manageBtn.type = 'button';
    manageBtn.className = 'btn btn-ghost';
    manageBtn.textContent = 'Manage';
    manageBtn.addEventListener('click', () => openPluginDetail(p));

    const verBtn = document.createElement('button');
    verBtn.type = 'button';
    verBtn.className = 'btn btn-ghost';
    verBtn.textContent = 'New version';
    verBtn.addEventListener('click', () => selectPublishVersion(p.id));

    actions.appendChild(manageBtn);
    actions.appendChild(verBtn);
    li.appendChild(meta);
    li.appendChild(actions);
    list.appendChild(li);
  });
}

function formatRemovedAt(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? String(iso ?? '') : d.toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z');
}

function removeMode() {
  return document.querySelector('input[name="remove-mode"]:checked')?.value || 'tombstone';
}

// The reason is what a 410 tells the world, so it is only required when a tombstone carries it.
function syncRemoveMode() {
  const erase = removeMode() === 'erase';
  document.getElementById('removal-reason').required = !erase;
  document.getElementById('removal-reason-note').textContent = erase ? '(optional, only kept in the service log)' : '';
  document.getElementById('remove-submit').textContent = erase ? 'Erase plugin' : 'Remove plugin';
}

function resetRemoveForm() {
  const tombstone = document.querySelector('input[name="remove-mode"][value="tombstone"]');
  if (tombstone) tombstone.checked = true;
  document.getElementById('removal-reason').value = '';
  syncRemoveMode();
}

// Erasing leaves no tombstone and no way back, so the id has to be typed.
function confirmErase(id) {
  const typed = window.prompt(
    `Erase "${id}" completely?\n\nEvery version and the plugin record are deleted and nothing is kept: ` +
      'the id becomes free and the plugin cannot be restored.\n\nType the plugin id to confirm.',
  );
  return typed !== null && typed.trim() === id;
}

async function erasePluginRecord(plugin) {
  const errEl = document.getElementById('removed-error');
  errEl.textContent = '';
  setAlert('removed-success', '', false);
  if (!confirmErase(plugin.id)) {
    errEl.textContent = 'The plugin id did not match, so nothing was erased.';
    return;
  }
  try {
    const res = await api(`/plugins/${encodeURIComponent(plugin.id)}`, {
      method: 'DELETE',
      body: { leaveTombstone: false },
    });
    setAlert('removed-success', `Erased ${res.id}. The id is free; its repository can publish to it again.`, true);
    await loadRemoved();
  } catch (err) {
    errEl.textContent = formatApiError(err);
    if (err?.status === 404) {
      errEl.textContent += '\nThe list was out of date and has been refreshed.';
      await loadRemoved().catch(() => {});
    }
  }
}

async function restorePlugin(plugin) {
  const errEl = document.getElementById('removed-error');
  errEl.textContent = '';
  setAlert('removed-success', '', false);
  const ok = window.confirm(
    `Restore plugin id "${plugin.id}"?\n\nThis reopens the id so its repository can publish again. ` +
      'The versions deleted by the removal are not recovered.',
  );
  if (!ok) return;
  try {
    const res = await api(`/plugins/${encodeURIComponent(plugin.id)}/restore`, { method: 'POST' });
    setAlert('removed-success', `Restored ${res.id}. It will appear in the catalogue after its next publish.`, true);
    await loadRemoved();
  } catch (err) {
    errEl.textContent = formatApiError(err);
    // 409/404: somebody already restored or erased it. Refresh so the stale row goes, and say so
    // rather than letting it vanish without a word.
    if (err?.status === 409 || err?.status === 404) {
      errEl.textContent += '\nThe list was out of date and has been refreshed.';
      await loadRemoved().catch(() => {});
    }
  }
}

async function loadRemoved() {
  const errEl = document.getElementById('removed-error');
  const list = document.getElementById('removed-list');
  const empty = document.getElementById('removed-empty');
  try {
    const data = await api('/removed-plugins');
    const plugins = data.plugins || [];
    list.innerHTML = '';
    empty.classList.toggle('hidden', plugins.length > 0);
    list.classList.toggle('hidden', plugins.length === 0);

    plugins.forEach((p) => {
      const li = document.createElement('li');
      const meta = document.createElement('div');
      meta.className = 'plugin-meta';
      meta.innerHTML =
        `<strong>${escapeHtml(p.id)}</strong>${badgeHtml('removed', 'neutral')}<br>` +
        `<small>${escapeHtml(formatRemovedAt(p.removed))} · by ${escapeHtml(p.removedBy)}</small><br>` +
        `<small>Reason: ${escapeHtml(p.removalReason)}</small>`;

      const actions = document.createElement('div');
      actions.className = 'row-actions';
      const restoreBtn = document.createElement('button');
      restoreBtn.type = 'button';
      restoreBtn.className = 'btn btn-ghost';
      restoreBtn.textContent = 'Restore';
      restoreBtn.addEventListener('click', () => restorePlugin(p));
      const eraseBtn = document.createElement('button');
      eraseBtn.type = 'button';
      eraseBtn.className = 'btn btn-danger';
      eraseBtn.textContent = 'Erase';
      eraseBtn.title = 'Delete the tombstone too: the id becomes free, as if never used';
      eraseBtn.addEventListener('click', () => erasePluginRecord(p));
      actions.appendChild(restoreBtn);
      actions.appendChild(eraseBtn);

      li.appendChild(meta);
      li.appendChild(actions);
      list.appendChild(li);
    });
  } catch (err) {
    errEl.textContent = formatApiError(err);
  }
}

async function loadLicenses() {
  const data = await api('/licenses');
  const list = document.getElementById('license-list');
  const empty = document.getElementById('license-empty');
  list.innerHTML = '';
  const entitlements = data.entitlements || [];
  empty.classList.toggle('hidden', entitlements.length > 0);
  list.classList.toggle('hidden', entitlements.length === 0);

  entitlements.forEach((e) => {
    const li = document.createElement('li');
    const meta = document.createElement('div');
    meta.className = 'plugin-meta';
    meta.innerHTML =
      `<strong>${escapeHtml(e.customerId)}</strong>` +
      (e.tier ? badgeHtml(e.tier, 'info') : '') +
      `<br><small>${e.publicKeys?.length || 0} keys` +
      (e.expiresAt ? ` · expires ${escapeHtml(e.expiresAt)}` : '') +
      `</small>`;

    const actions = document.createElement('div');
    actions.className = 'row-actions';
    const rotateBtn = document.createElement('button');
    rotateBtn.type = 'button';
    rotateBtn.className = 'btn btn-ghost';
    rotateBtn.textContent = 'Rotate key';
    rotateBtn.title = 'Add a new key alongside the existing ones; the new private key is shown once';
    rotateBtn.addEventListener('click', () => rotateLicenseKey(e));
    const revokeBtn = document.createElement('button');
    revokeBtn.type = 'button';
    revokeBtn.className = 'btn btn-danger';
    revokeBtn.textContent = 'Revoke';
    revokeBtn.addEventListener('click', () => revokeLicense(e));
    actions.appendChild(rotateBtn);
    actions.appendChild(revokeBtn);

    li.appendChild(meta);
    li.appendChild(actions);
    list.appendChild(li);
  });
}

function resetLicenseAlerts() {
  document.getElementById('license-error').textContent = '';
  setAlert('license-success', '', false);
  hideBox('license-result-box');
}

async function revokeLicense(entitlement) {
  resetLicenseAlerts();
  const ok = window.confirm(
    `Revoke the entitlement for "${entitlement.customerId}"?\n\n` +
      'Every license key issued to this customer stops working.',
  );
  if (!ok) return;
  try {
    await api(`/licenses/${encodeURIComponent(entitlement.customerId)}`, { method: 'DELETE' });
    setAlert('license-success', `Entitlement revoked for ${entitlement.customerId}`, true);
  } catch (err) {
    document.getElementById('license-error').textContent = formatApiError(err);
  }
  await loadLicenses().catch(() => {});
}

async function rotateLicenseKey(entitlement) {
  resetLicenseAlerts();
  try {
    const res = await api(`/licenses/${encodeURIComponent(entitlement.customerId)}/keys`, { method: 'POST' });
    setAlert(
      'license-success',
      `New key issued for ${res.customerId || entitlement.customerId}. Copy the private key now; it is not shown again.`,
      true,
    );
    document.getElementById('license-result').textContent = JSON.stringify(res, null, 2);
    showBox('license-result-box', true);
  } catch (err) {
    document.getElementById('license-error').textContent = formatApiError(err);
  }
  await loadLicenses().catch(() => {});
}

init();
