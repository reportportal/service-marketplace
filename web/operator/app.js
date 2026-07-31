const API = '/api/v1';

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

  try {
    await loadPlugins();
    loginSection.classList.add('hidden');
    consoleSection.classList.remove('hidden');
    document.getElementById('auth-status').textContent = 'Signed in';
  } catch {
    loginSection.classList.remove('hidden');
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
    await api('/auth/logout', { method: 'POST' });
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
    li.innerHTML = `<strong>${p.name}</strong> (${p.id}) v${p.latestVersion}<br><small>${p.category} · ${p.access} · ${p.tier}</small>`;
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

function debounce(fn, ms) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}

init();
