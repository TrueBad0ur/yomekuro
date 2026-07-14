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

const SETTINGS_SECTIONS = ['libraries', 'upload', 'books', 'users-section'];

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
  renderBooksLibraryPicker(libs.items || []);

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

function setUploadFiles(files) {
  const n = files ? files.length : 0;
  uploadFilenameLabel.textContent =
    n === 0 ? 'No file selected' :
    n === 1 ? files[0].name :
    `${n} files selected`;
  uploadFilenameLabel.classList.toggle('has-file', n > 0);
}

uploadFileInput.addEventListener('change', () => setUploadFiles(uploadFileInput.files));

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
  if (!e.dataTransfer.files.length) return;
  uploadFileInput.files = e.dataTransfer.files;
  setUploadFiles(uploadFileInput.files);
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

// One request per file, so each becomes its own queued job. Sequential: with one
// GPU behind the queue, parallel uploads would only fight over bandwidth.
function uploadOne(file, libraryId, addExisting, existingSeries, name, onProgress) {
  return new Promise((resolve) => {
    const form = new FormData();
    form.append('library_id', libraryId);
    if (addExisting) {
      form.append('existing_series', existingSeries);
    } else if (name) {
      form.append('name', name);
    }
    form.append('file', file);

    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/converter/upload');
    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100), false);
    });
    // Transfer done, but the server is still staging and queuing — show a moving
    // bar rather than a stalled 100% one.
    xhr.upload.addEventListener('load', () => onProgress(100, true));
    xhr.addEventListener('load', () => {
      if (xhr.status >= 200 && xhr.status < 300) { resolve(null); return; }
      let msg = 'Upload failed';
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch {}
      resolve(msg);
    });
    xhr.addEventListener('error', () => resolve('Upload failed'));
    xhr.send(form);
  });
}

document.getElementById('btn-upload').addEventListener('click', async () => {
  const errEl = document.getElementById('upload-error');
  errEl.hidden = true;
  const libraryId = selectedUploadLibraryId;
  const addExisting = uploadAddExisting.checked;
  const name = document.getElementById('upload-name').value.trim();
  const existingSeries = uploadSeriesPicker.value;
  const fileInput = uploadFileInput;
  const files = Array.from(fileInput.files);
  if (!libraryId || files.length === 0) {
    errEl.textContent = 'Pick a library and at least one file';
    errEl.hidden = false;
    return;
  }
  if (addExisting && !existingSeries) {
    errEl.textContent = 'Pick a book to add to';
    errEl.hidden = false;
    return;
  }

  const btn = document.getElementById('btn-upload');
  btn.disabled = true;

  const failures = [];
  for (let i = 0; i < files.length; i++) {
    const f = files[i];
    const prefix = files.length > 1 ? `(${i + 1}/${files.length}) ${f.name}: ` : '';
    setUploadProgress(0, `${prefix}Uploading… 0%`, false);
    // A typed name only makes sense for a single new book; with several files
    // each one is named after itself.
    const perFileName = (!addExisting && files.length === 1) ? name : '';
    const err = await uploadOne(f, libraryId, addExisting, existingSeries, perFileName,
      (pct, staging) => setUploadProgress(pct,
        staging ? `${prefix}Processing…` : `${prefix}Uploading… ${pct}%`, staging));
    if (err) failures.push(`${f.name}: ${err}`);
    loadConversionJobs();
  }

  btn.disabled = false;
  hideUploadProgress();
  document.getElementById('upload-name').value = '';
  fileInput.value = '';
  setUploadFiles(null);
  if (failures.length === 0) {
    uploadAddExisting.checked = false;
    uploadNameField.hidden = false;
    uploadSeriesField.hidden = true;
  } else {
    errEl.textContent = failures.join(' · ');
    errEl.hidden = false;
  }
  loadConversionJobs();
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
    // A live job's button requests a clean stop (the worker cancels mokuro); a
    // terminal one has nothing to stop, so the same button just clears the row.
    const stoppable = j.status === 'pending' || j.status === 'running';
    // "Stopping…" only while live: a terminal job with a stale flag would sit
    // here disabled forever.
    const stopping = j.stop_requested && stoppable;
    const label = stopping ? 'Stopping…' : (stoppable ? 'Stop' : 'Remove');
    return `
    <div class="library-card">
      <div class="library-card-name">${esc(j.name)}${j.force_ocr ? ` <span class="job-reconvert-badge">${j.volume ? 'OCR re-run: ' + esc(j.volume) : 'OCR re-run'}</span>` : ''}</div>
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

// ── Books: per-series full-OCR reconvert ────────────────────────────────────────

const booksLibraryPicker = document.getElementById('books-library-picker');
let selectedBooksLibraryId = '';

function renderBooksLibraryPicker(libraries) {
  booksLibraryPicker.innerHTML = '';
  if (!selectedBooksLibraryId && libraries.length > 0) {
    selectedBooksLibraryId = libraries[0].id;
  }
  for (const lib of libraries) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'library-pick-btn';
    btn.classList.toggle('active', lib.id === selectedBooksLibraryId);
    btn.textContent = lib.name;
    btn.addEventListener('click', () => {
      selectedBooksLibraryId = lib.id;
      booksLibraryPicker.querySelectorAll('.library-pick-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      loadBooksList();
    });
    booksLibraryPicker.appendChild(btn);
  }
  loadBooksList();
}

async function loadBooksList() {
  const list = document.getElementById('books-list');
  if (!selectedBooksLibraryId) { list.innerHTML = ''; return; }
  list.innerHTML = '<p style="color:var(--text-dim);font-size:.88rem">Loading…</p>';
  let data;
  try {
    data = await fetch(`/api/converter/reconvertable?library=${selectedBooksLibraryId}`).then(r => r.json());
  } catch {
    list.innerHTML = '<p style="color:var(--text-dim)">Failed to load books.</p>';
    return;
  }
  const items = data.items || [];
  if (items.length === 0) {
    list.innerHTML = '<p style="color:var(--text-dim);font-size:.88rem">No books in this library.</p>';
    return;
  }
  list.innerHTML = items.map(s => {
    if (s.kind === 'html') {
      // A standalone HTML file: one book, one file, one obvious format — no
      // picker, no reconvert (never went through this app's conversion at all).
      return `
      <div class="library-card">
        <div class="library-card-name">${esc(s.name)}</div>
        <div class="library-card-footer">
          <span class="library-card-count">HTML file</span>
          <button class="reconvert-btn dl-btn" data-name="${esc(s.name)}" data-volume="${esc(s.name)}" data-kind="html" data-format="html">Download HTML</button>
          <button class="job-delete-btn book-delete-btn" data-name="${esc(s.name)}" data-kind="html">Delete</button>
        </div>
      </div>`;
    }

    const volumeRows = s.volumes.map(v => `
      <div class="reconvert-volume-row">
        <span class="reconvert-volume-name">${esc(v.name)}</span>
        <div class="reconvert-volume-actions">
          ${v.has_images ? `
            <select class="dl-format-picker" aria-label="Download format">
              <option value="images">Images (.zip)</option>
              <option value="pdf">PDF</option>
              <option value="epub">EPUB</option>
            </select>
            <button class="reconvert-btn dl-btn" data-name="${esc(s.name)}" data-volume="${esc(v.name)}">Download</button>
          ` : `
            <button class="reconvert-btn dl-btn" data-name="${esc(s.name)}" data-volume="${esc(v.name)}" data-format="epub">Download EPUB</button>
          `}
          ${s.has_raw_scan ? `<button class="reconvert-btn" data-name="${esc(s.name)}" data-volume="${esc(v.name)}">Reconvert (full OCR)</button>` : ''}
        </div>
      </div>`).join('');
    return `
    <div class="library-card">
      <div class="library-card-name">${esc(s.name)}</div>
      <div class="library-card-footer">
        <span class="library-card-count">${s.volumes.length} volume${s.volumes.length === 1 ? '' : 's'}</span>
        ${s.has_raw_scan
          ? `<button class="reconvert-btn" data-name="${esc(s.name)}">Reconvert all (full OCR)</button>`
          : `<span class="reconvert-no-scan" title="Raw scan no longer on disk — reconvert needs a fresh upload">no raw scan</span>`}
        <button class="job-delete-btn book-delete-btn" data-name="${esc(s.name)}">Delete</button>
      </div>
      <p class="reconvert-error" style="color:#e07070;font-size:.78rem;margin:.35rem 0 0" hidden></p>
      ${volumeRows ? `<div class="reconvert-volumes">${volumeRows}</div>` : ''}
    </div>`;
  }).join('');
}

document.getElementById('books-list').addEventListener('click', async (e) => {
  const dlBtn = e.target.closest('.dl-btn');
  if (dlBtn) {
    // A fixed data-format (HTML file, or a no-images EPUB-only row) skips the
    // picker entirely; otherwise read the format the user picked next to it.
    let format = dlBtn.dataset.format;
    if (!format) {
      const row = dlBtn.closest('.reconvert-volume-row');
      format = row.querySelector('.dl-format-picker').value;
    }
    let url = `/api/converter/extract-images?library=${encodeURIComponent(selectedBooksLibraryId)}` +
      `&name=${encodeURIComponent(dlBtn.dataset.name)}&volume=${encodeURIComponent(dlBtn.dataset.volume)}` +
      `&format=${encodeURIComponent(format)}`;
    if (dlBtn.dataset.kind) url += `&kind=${encodeURIComponent(dlBtn.dataset.kind)}`;
    window.location.href = url;
    return;
  }

  const delBtn = e.target.closest('.book-delete-btn');
  if (delBtn) {
    const name = delBtn.dataset.name;
    if (!confirm(`Permanently delete "${name}"? This removes the EPUB(s) and the raw scan from disk — cannot be undone.`)) {
      return;
    }
    delBtn.disabled = true;
    delBtn.textContent = 'Deleting…';
    let url = `/api/converter/books?library=${encodeURIComponent(selectedBooksLibraryId)}&name=${encodeURIComponent(name)}`;
    if (delBtn.dataset.kind) url += `&kind=${encodeURIComponent(delBtn.dataset.kind)}`;
    try {
      const res = await fetch(url, { method: 'DELETE' });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        alert(d.error || 'Failed to delete');
        delBtn.disabled = false;
        delBtn.textContent = 'Delete';
        return;
      }
      loadBooksList();
    } catch {
      alert('Failed to delete');
      delBtn.disabled = false;
      delBtn.textContent = 'Delete';
    }
    return;
  }

  const btn = e.target.closest('button.reconvert-btn:not(.dl-btn)');
  if (!btn) return;
  const card = btn.closest('.library-card');
  const errEl = card.querySelector('.reconvert-error');
  const label = btn.dataset.volume ? 'Reconvert (full OCR)' : 'Reconvert all (full OCR)';
  errEl.hidden = true;
  btn.disabled = true;
  btn.textContent = 'Queuing…';
  try {
    const res = await fetch('/api/converter/reconvert', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        library_id: selectedBooksLibraryId,
        name: btn.dataset.name,
        volume: btn.dataset.volume || '',
      }),
    });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      errEl.textContent = d.error || 'Failed to queue reconvert';
      errEl.hidden = false;
      btn.disabled = false;
      btn.textContent = label;
      return;
    }
    btn.textContent = 'Queued';
    loadConversionJobs();
  } catch {
    errEl.textContent = 'Failed to queue reconvert';
    errEl.hidden = false;
    btn.disabled = false;
    btn.textContent = label;
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
