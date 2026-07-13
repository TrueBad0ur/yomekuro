'use strict';

// ── Auth guard ────────────────────────────────────────────────────────────────

let currentUser = null;

fetch('/api/auth/me').then(async r => {
  if (!r.ok) { location.href = '/login'; return; }
  currentUser = await r.json();
  init();
}).catch(() => { location.href = '/login'; });

function init() {
  loadLibraries();
  loadConversionJobs();
  fetch('/api/config').then(r => r.json()).then(cfg => {
    setInterval(loadConversionJobs, cfg.jobs_poll_interval_ms || 20000);
  }).catch(() => {
    setInterval(loadConversionJobs, 20000);
  });
  loadUsers();
  showSettingsSection(location.hash.slice(1));
}

// ── Settings nav (one category visible at a time) ────────────────────────────

const SETTINGS_SECTIONS = ['libraries', 'upload', 'users-section'];

function showSettingsSection(id) {
  if (!SETTINGS_SECTIONS.includes(id)) id = 'libraries';

  document.querySelectorAll('.settings-section').forEach(sec => {
    sec.classList.toggle('active', sec.id === id);
  });
  document.querySelectorAll('.settings-nav-link').forEach(link => {
    link.classList.toggle('active', link.getAttribute('href') === '#' + id);
  });
  history.replaceState(null, '', '#' + id);
}

document.querySelectorAll('.settings-nav-link').forEach(link => {
  link.addEventListener('click', (e) => {
    e.preventDefault();
    showSettingsSection(link.getAttribute('href').slice(1));
  });
});

// ── Logout ────────────────────────────────────────────────────────────────────

document.getElementById('btn-logout').addEventListener('click', async () => {
  await fetch('/api/auth/logout', { method: 'POST' });
  location.href = '/login';
});

// ── Libraries ─────────────────────────────────────────────────────────────────

async function loadLibraries() {
  const list = document.getElementById('libraries-list');
  let libs;
  try {
    libs = await fetch('/api/libraries').then(r => r.json());
  } catch {
    list.innerHTML = '<p style="color:var(--text-dim)">Failed to load libraries.</p>';
    return;
  }

  list.innerHTML = '';

  renderUploadLibraryPicker(libs.items || []);

  for (const lib of (libs.items || [])) {
    const card = document.createElement('div');
    card.className = 'library-card';
    card.innerHTML = `
      <div class="library-card-name">${esc(lib.name)}</div>
      <div class="library-card-path">${esc(lib.path)}</div>
      <div class="library-card-footer">
        <span class="library-card-count" id="lc-${lib.id}">—</span>
        <button class="sync-btn" id="sync-${lib.id}">Sync</button>
      </div>`;
    list.appendChild(card);

    fetch(`/api/books?library=${lib.id}&limit=1`)
      .then(r => r.json())
      .then(d => {
        const el = document.getElementById(`lc-${lib.id}`);
        if (el) el.textContent = `${d.total} books`;
      }).catch(() => {});

    card.querySelector('.sync-btn').addEventListener('click', async function() {
      this.disabled = true;
      this.textContent = 'Syncing…';
      try {
        await fetch(`/api/libraries/${lib.id}/scan`, { method: 'POST' });
        await new Promise(r => setTimeout(r, 3000));
        const d = await fetch(`/api/books?library=${lib.id}&limit=1`).then(r => r.json());
        const el = document.getElementById(`lc-${lib.id}`);
        if (el) el.textContent = `${d.total} books`;
        this.textContent = 'Done';
        setTimeout(() => { this.textContent = 'Sync'; this.disabled = false; }, 1500);
      } catch {
        this.textContent = 'Sync';
        this.disabled = false;
      }
    });
  }
}

// ── Add Library ───────────────────────────────────────────────────────────────

document.getElementById('btn-add-library').addEventListener('click', async () => {
  const errEl = document.getElementById('add-library-error');
  errEl.hidden = true;
  const name = document.getElementById('lib-name').value.trim();
  const path = document.getElementById('lib-path').value.trim();
  if (!name || !path) {
    errEl.textContent = 'Name and path are required';
    errEl.hidden = false;
    return;
  }
  const btn = document.getElementById('btn-add-library');
  btn.disabled = true;
  try {
    const res = await fetch('/api/libraries', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, path }),
    });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      errEl.textContent = d.error || 'Failed';
      errEl.hidden = false;
      return;
    }
    const lib = await res.json();
    document.getElementById('lib-name').value = '';
    document.getElementById('lib-path').value = '';
    await loadLibraries();
    // Auto-trigger scan after adding
    fetch(`/api/libraries/${lib.id}/scan`, { method: 'POST' }).catch(() => {});
  } finally {
    btn.disabled = false;
  }
});

// ── Manga upload + conversion jobs ──────────────────────────────────────────────

const uploadProgressRow   = document.getElementById('upload-progress-row');
const uploadProgressFill  = document.getElementById('upload-progress-fill');
const uploadProgressLabel = document.getElementById('upload-progress-label');
const uploadLibraryPicker = document.getElementById('upload-library-picker');
const uploadDropzone      = document.getElementById('upload-dropzone');
const uploadFileInput     = document.getElementById('upload-file');
const uploadFilenameLabel = document.getElementById('upload-filename');
const uploadAddExisting   = document.getElementById('upload-add-existing');
const uploadNameField     = document.getElementById('upload-name-field');
const uploadSeriesField   = document.getElementById('upload-series-field');
const uploadSeriesPicker  = document.getElementById('upload-series-picker');

let selectedUploadLibraryId = '';

function renderUploadLibraryPicker(libraries) {
  uploadLibraryPicker.innerHTML = '';
  if (!selectedUploadLibraryId && libraries.length > 0) {
    selectedUploadLibraryId = libraries[0].id;
  }
  for (const lib of libraries) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'library-pick-btn';
    btn.classList.toggle('active', lib.id === selectedUploadLibraryId);
    btn.textContent = lib.name;
    btn.addEventListener('click', () => {
      selectedUploadLibraryId = lib.id;
      uploadLibraryPicker.querySelectorAll('.library-pick-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      loadUploadSeriesPicker();
    });
    uploadLibraryPicker.appendChild(btn);
  }
  loadUploadSeriesPicker();
}

async function loadUploadSeriesPicker() {
  if (!uploadAddExisting.checked || !selectedUploadLibraryId) return;
  uploadSeriesPicker.innerHTML = '<option>Loading…</option>';
  try {
    const data = await fetch(`/api/series?library=${selectedUploadLibraryId}`).then(r => r.json());
    const items = data.items || [];
    uploadSeriesPicker.innerHTML = items.length === 0
      ? '<option value="">No books in this library</option>'
      : items.map(s => `<option value="${esc(s.name)}">${esc(s.name)} (${s.book_count})</option>`).join('');
  } catch {
    uploadSeriesPicker.innerHTML = '<option value="">Failed to load</option>';
  }
}

uploadAddExisting.addEventListener('change', () => {
  const on = uploadAddExisting.checked;
  uploadNameField.hidden = on;
  uploadSeriesField.hidden = !on;
  if (on) loadUploadSeriesPicker();
});

function setUploadFile(file) {
  uploadFilenameLabel.textContent = file ? file.name : 'No file selected';
  uploadFilenameLabel.classList.toggle('has-file', !!file);
}

uploadFileInput.addEventListener('change', () => setUploadFile(uploadFileInput.files[0] || null));

['dragenter', 'dragover'].forEach(evt => {
  uploadDropzone.addEventListener(evt, (e) => {
    e.preventDefault();
    uploadDropzone.classList.add('dragover');
  });
});
['dragleave', 'dragend'].forEach(evt => {
  uploadDropzone.addEventListener(evt, () => uploadDropzone.classList.remove('dragover'));
});
uploadDropzone.addEventListener('drop', (e) => {
  e.preventDefault();
  uploadDropzone.classList.remove('dragover');
  const file = e.dataTransfer.files[0];
  if (!file) return;
  uploadFileInput.files = e.dataTransfer.files;
  setUploadFile(file);
});

function setUploadProgress(pct, label, indeterminate) {
  uploadProgressRow.hidden = false;
  uploadProgressFill.classList.toggle('indeterminate', !!indeterminate);
  uploadProgressFill.style.width = indeterminate ? '' : pct + '%';
  uploadProgressLabel.textContent = label;
}

function hideUploadProgress() {
  uploadProgressRow.hidden = true;
  uploadProgressFill.classList.remove('indeterminate');
  uploadProgressFill.style.width = '0%';
}

document.getElementById('btn-upload').addEventListener('click', () => {
  const errEl = document.getElementById('upload-error');
  errEl.hidden = true;
  const libraryId = selectedUploadLibraryId;
  const addExisting = uploadAddExisting.checked;
  const name = document.getElementById('upload-name').value.trim();
  const existingSeries = uploadSeriesPicker.value;
  const fileInput = uploadFileInput;
  const file = fileInput.files[0];
  if (!libraryId || !file) {
    errEl.textContent = 'Pick a library and a file';
    errEl.hidden = false;
    return;
  }
  if (addExisting && !existingSeries) {
    errEl.textContent = 'Pick a book to add to';
    errEl.hidden = false;
    return;
  }

  const form = new FormData();
  form.append('library_id', libraryId);
  if (addExisting) {
    form.append('existing_series', existingSeries);
  } else if (name) {
    form.append('name', name);
  }
  form.append('file', file);

  const btn = document.getElementById('btn-upload');
  btn.disabled = true;
  setUploadProgress(0, 'Uploading… 0%', false);

  const xhr = new XMLHttpRequest();
  xhr.open('POST', '/api/converter/upload');

  xhr.upload.addEventListener('progress', (e) => {
    if (!e.lengthComputable) return;
    const pct = Math.round((e.loaded / e.total) * 100);
    setUploadProgress(pct, `Uploading… ${pct}%`, false);
  });

  xhr.upload.addEventListener('load', () => {
    // Transfer done; server is now extracting/staging and queuing the job
    // (no byte-level progress for that part) — show a moving bar instead of
    // a stalled 100% one.
    setUploadProgress(100, 'Processing…', true);
  });

  xhr.addEventListener('load', () => {
    btn.disabled = false;
    if (xhr.status >= 200 && xhr.status < 300) {
      hideUploadProgress();
      document.getElementById('upload-name').value = '';
      uploadAddExisting.checked = false;
      uploadNameField.hidden = false;
      uploadSeriesField.hidden = true;
      fileInput.value = ''; setUploadFile(null);
      loadConversionJobs();
      return;
    }
    hideUploadProgress();
    document.getElementById('upload-name').value = '';
    fileInput.value = ''; setUploadFile(null);
    let msg = 'Upload failed';
    try { msg = JSON.parse(xhr.responseText).error || msg; } catch {}
    errEl.textContent = msg;
    errEl.hidden = false;
  });

  xhr.addEventListener('error', () => {
    btn.disabled = false;
    hideUploadProgress();
    document.getElementById('upload-name').value = '';
    fileInput.value = ''; setUploadFile(null);
    errEl.textContent = 'Upload failed';
    errEl.hidden = false;
  });

  xhr.send(form);
});

async function loadConversionJobs() {
  const list = document.getElementById('conversion-jobs');
  let data;
  try {
    data = await fetch('/api/converter/jobs').then(r => r.json());
  } catch {
    return;
  }
  const jobs = data.items || [];
  if (jobs.length === 0) {
    list.innerHTML = '';
    return;
  }
  list.innerHTML = jobs.map(j => {
    // A pending/running job is still doing (or about to do) real work —
    // clicking the button there requests a clean stop (converter-worker
    // cancels the mokuro subprocess itself; see internal/api/converter.go's
    // deleteConversionJob). A terminal job (done/failed/stopped) has nothing
    // left to stop, so the same button just clears its row.
    const stoppable = j.status === 'pending' || j.status === 'running';
    // "Stopping…" only while the job is still live: a terminal job carrying a
    // stale stop_requested would otherwise sit here disabled forever, with no
    // way to clear it.
    const stopping = j.stop_requested && stoppable;
    const label = stopping ? 'Stopping…' : (stoppable ? 'Stop' : 'Remove');
    return `
    <div class="library-card">
      <div class="library-card-name">${esc(j.name)}</div>
      ${j.status === 'running' && j.current_volume ? `<div class="job-current-volume">${esc(j.current_volume)}</div>` : ''}
      <div class="library-card-footer">
        <span class="library-card-count job-status-${esc(j.status)}">${esc(j.status)}</span>
        ${j.error ? `<span style="color:#e07070;font-size:.78rem">${esc(j.error)}</span>` : ''}
        <button class="job-delete-btn" data-id="${esc(j.id)}" ${stopping ? 'disabled' : ''}
          title="${stoppable ? 'Stop this conversion' : 'Remove from list'}">${label}</button>
      </div>
    </div>`;
  }).join('');
}

document.getElementById('conversion-jobs').addEventListener('click', async (e) => {
  const btn = e.target.closest('.job-delete-btn');
  if (!btn) return;
  btn.disabled = true;
  try {
    await fetch(`/api/converter/jobs/${btn.dataset.id}`, { method: 'DELETE' });
    loadConversionJobs();
  } catch {
    btn.disabled = false;
  }
});

// ── Users (admin only) ────────────────────────────────────────────────────────

async function loadUsers() {
  const list = document.getElementById('users-list');
  let data;
  try {
    data = await fetch('/api/users').then(r => r.json());
  } catch {
    list.innerHTML = '<p style="color:var(--text-dim)">Failed to load users.</p>';
    return;
  }
  renderUsers(data.items || []);
}

function renderUsers(users) {
  const list = document.getElementById('users-list');
  list.innerHTML = '';
  for (const u of users) {
    const isSelf = currentUser && u.id === currentUser.id;

    // Main row
    const row = document.createElement('div');
    row.className = 'settings-row user-row';
    row.innerHTML = `
      <div class="user-info">
        <span class="user-name">${esc(u.username)}</span>
        ${u.is_admin ? '<span class="user-badge">Admin</span>' : ''}
        ${isSelf ? '<span class="user-badge user-badge-you">You</span>' : ''}
      </div>
      <div class="user-actions">
        ${!isSelf ? `
          <button class="user-pwd-btn" data-id="${u.id}">Change password</button>
          <button class="user-admin-toggle" data-id="${u.id}" data-admin="${u.is_admin}">
            ${u.is_admin ? 'Remove admin' : 'Make admin'}
          </button>
          <button class="user-del-btn" data-id="${u.id}">Delete</button>
        ` : ''}
      </div>`;
    list.appendChild(row);

    // Inline password row (hidden initially)
    const pwdRow = document.createElement('div');
    pwdRow.className = 'user-pwd-row';
    pwdRow.hidden = true;
    pwdRow.innerHTML = `
      <input type="password" class="user-input user-pwd-input" placeholder="New password">
      <button class="user-pwd-save" data-id="${u.id}">Save</button>
      <button class="user-pwd-cancel">Cancel</button>
      <span class="user-pwd-err" style="color:#e07070;font-size:.8rem"></span>`;
    list.appendChild(pwdRow);

    // Toggle password row
    const pwdBtn = row.querySelector('.user-pwd-btn');
    if (pwdBtn) {
      pwdBtn.addEventListener('click', () => {
        pwdRow.hidden = !pwdRow.hidden;
        if (!pwdRow.hidden) pwdRow.querySelector('input').focus();
      });
      pwdRow.querySelector('.user-pwd-cancel').addEventListener('click', () => {
        pwdRow.hidden = true;
        pwdRow.querySelector('input').value = '';
        pwdRow.querySelector('.user-pwd-err').textContent = '';
      });
      pwdRow.querySelector('.user-pwd-save').addEventListener('click', async (e) => {
        const id = e.target.dataset.id;
        const pwd = pwdRow.querySelector('input').value;
        const errEl = pwdRow.querySelector('.user-pwd-err');
        errEl.textContent = '';
        if (!pwd) { errEl.textContent = 'Enter a password'; return; }
        const res = await fetch(`/api/users/${id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ password: pwd }),
        });
        if (res.ok) {
          pwdRow.hidden = true;
          pwdRow.querySelector('input').value = '';
        } else {
          errEl.textContent = 'Failed';
        }
      });
    }

    // Toggle admin
    const adminBtn = row.querySelector('.user-admin-toggle');
    if (adminBtn) {
      adminBtn.addEventListener('click', async () => {
        const id = adminBtn.dataset.id;
        const makeAdmin = adminBtn.dataset.admin === 'false';
        adminBtn.disabled = true;
        const res = await fetch(`/api/users/${id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ is_admin: makeAdmin }),
        });
        if (res.ok) loadUsers();
        else adminBtn.disabled = false;
      });
    }

    // Delete
    const delBtn = row.querySelector('.user-del-btn');
    if (delBtn) {
      delBtn.addEventListener('click', async () => {
        if (!confirm(`Delete user "${u.username}"?`)) return;
        delBtn.disabled = true;
        const res = await fetch(`/api/users/${u.id}`, { method: 'DELETE' });
        if (res.ok) loadUsers();
        else delBtn.disabled = false;
      });
    }
  }
}

document.getElementById('btn-create-user').addEventListener('click', async () => {
  const errEl = document.getElementById('create-user-error');
  errEl.hidden = true;
  const username = document.getElementById('new-username').value.trim();
  const password = document.getElementById('new-password').value;
  const isAdmin  = document.getElementById('new-is-admin').checked;
  if (!username || !password) {
    errEl.textContent = 'Username and password required';
    errEl.hidden = false;
    return;
  }
  const res = await fetch('/api/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, is_admin: isAdmin }),
  });
  if (!res.ok) {
    const d = await res.json().catch(() => ({}));
    errEl.textContent = d.error || 'Failed';
    errEl.hidden = false;
    return;
  }
  document.getElementById('new-username').value = '';
  document.getElementById('new-password').value = '';
  document.getElementById('new-is-admin').checked = false;
  loadUsers();
});

// ── Helpers ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
