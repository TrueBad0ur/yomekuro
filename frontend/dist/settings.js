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
  loadSystemStatus();
  startSystemStatusPolling();
  showSettingsSection(location.hash.slice(1));
}

// ── Settings nav (one category visible at a time) ────────────────────────────

const SETTINGS_SECTIONS = ['libraries', 'upload', 'books', 'status', 'users-section'];

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
  updateQueueControls(jobs);
  if (jobs.length === 0) {
    list.innerHTML = '';
    return;
  }
  list.innerHTML = jobs.map(j => {
    // A live job's button requests a clean stop (the worker cancels mokuro); a
    // terminal one has nothing to stop, so the same button just clears the row.
    // 'paused' counts as terminal here too — nothing is running to stop, and
    // removing a paused row is now safe (never wipes its files, same as 'done').
    const stoppable = j.status === 'pending' || j.status === 'running';
    // "Stopping…" only while live: a terminal job with a stale flag would sit
    // here disabled forever.
    const stopping = j.stop_requested && stoppable;
    const label = stopping ? 'Stopping…' : (stoppable ? 'Stop' : 'Remove');
    return `
    <div class="library-card">
      <div class="library-card-name">${esc(j.name)}${j.force_ocr ? ` <span class="job-reconvert-badge">${j.volume ? 'OCR re-run: ' + esc(j.volume) : 'OCR re-run'}</span>` : ''}</div>
      ${(j.status === 'running' || j.status === 'paused') && j.current_volume ? `<div class="job-current-volume">${j.status === 'paused' ? 'Paused at ' : ''}${esc(j.current_volume)}</div>` : ''}
      <div class="library-card-footer">
        <span class="library-card-count job-status-${esc(j.status)}">${esc(j.status)}</span>
        ${j.error ? `<span style="color:#e07070;font-size:.78rem">${esc(j.error)}</span>` : ''}
        <button class="job-delete-btn" data-id="${esc(j.id)}" ${stopping ? 'disabled' : ''}
          title="${stoppable ? 'Stop this conversion' : 'Remove from list'}">${label}</button>
      </div>
    </div>`;
  }).join('');
}

function updateQueueControls(jobs) {
  const pauseBtn = document.getElementById('btn-pause-queue');
  const resumeBtn = document.getElementById('btn-resume-queue');
  const hint = document.getElementById('queue-controls-hint');
  if (!pauseBtn || !resumeBtn) return;

  const pausableCount = jobs.filter(j =>
    (j.status === 'pending') || (j.status === 'running' && !j.current_volume)
  ).length;
  const pausedCount = jobs.filter(j => j.status === 'paused').length;

  pauseBtn.disabled = pausableCount === 0;
  resumeBtn.disabled = pausedCount === 0;
  hint.textContent = pausedCount > 0 ? `${pausedCount} paused` : '';
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

document.getElementById('btn-pause-queue').addEventListener('click', async function() {
  this.disabled = true;
  this.textContent = 'Pausing…';
  try {
    await fetch('/api/converter/queue/pause', { method: 'POST' });
  } catch {
    alert('Failed to pause queue');
  }
  this.textContent = 'Pause Queue';
  loadConversionJobs();
});

document.getElementById('btn-resume-queue').addEventListener('click', async function() {
  this.disabled = true;
  this.textContent = 'Resuming…';
  try {
    await fetch('/api/converter/queue/resume', { method: 'POST' });
  } catch {
    alert('Failed to resume queue');
  }
  this.textContent = 'Resume Queue';
  loadConversionJobs();
});

// ── Books: per-series full-OCR reconvert ────────────────────────────────────────

const booksLibraryPicker = document.getElementById('books-library-picker');
let selectedBooksLibraryId = '';
let booksListData = [];
let booksExpanded = new Set();
const booksSearchInput = document.getElementById('books-search');
if (booksSearchInput) {
  booksSearchInput.addEventListener('input', () => renderBooksList());
}

function formatAnalyzed(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d)) return '';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

function lastAnalyzed(item) {
  const dates = item.volumes.map(v => v.modified_at).filter(Boolean);
  if (dates.length === 0) return '';
  return dates.reduce((a, b) => (a > b ? a : b));
}

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
  if (!selectedBooksLibraryId) { list.innerHTML = ''; booksListData = []; return; }
  list.innerHTML = '<p style="color:var(--text-dim);font-size:.88rem">Loading…</p>';
  try {
    const data = await fetch(`/api/converter/reconvertable?library=${selectedBooksLibraryId}`).then(r => r.json());
    booksListData = data.items || [];
  } catch {
    list.innerHTML = '<p style="color:var(--text-dim)">Failed to load books.</p>';
    return;
  }
  renderBooksList();
}

// Rendered from the cached booksListData rather than re-fetching, so typing in
// the search box or expanding/collapsing a book is instant. The list is kept
// collapsed to a one-line header per book by default — a growing library
// otherwise turns this into an endless per-volume scroll — and only expands
// to show per-volume rows on click.
function renderBooksList() {
  const list = document.getElementById('books-list');
  if (booksListData.length === 0) {
    list.innerHTML = '<p style="color:var(--text-dim);font-size:.88rem">No books in this library.</p>';
    return;
  }
  const query = (booksSearchInput?.value || '').trim().toLowerCase();
  const items = query ? booksListData.filter(s => s.name.toLowerCase().includes(query)) : booksListData;
  if (items.length === 0) {
    list.innerHTML = '<p style="color:var(--text-dim);font-size:.88rem">No books match your search.</p>';
    return;
  }
  list.innerHTML = items.map(s => {
    const analyzed = lastAnalyzed(s);
    const analyzedLabel = analyzed ? `<span class="library-card-analyzed" title="Last analyzed">Analyzed ${formatAnalyzed(analyzed)}</span>` : '';

    if (s.kind === 'html') {
      // A standalone HTML file: one book, one file, one obvious format — no
      // picker, no reconvert (never went through this app's conversion at all).
      return `
      <div class="library-card">
        <div class="library-card-name">${esc(s.name)}</div>
        <div class="library-card-footer">
          <span class="library-card-count">HTML file</span>
          ${analyzedLabel}
          <button class="reconvert-btn dl-btn" data-name="${esc(s.name)}" data-volume="${esc(s.name)}" data-kind="html" data-format="html">Download HTML</button>
          <button class="reconvert-btn book-rename-btn" data-name="${esc(s.name)}" data-kind="html">Rename</button>
          <button class="job-delete-btn book-delete-btn" data-name="${esc(s.name)}" data-kind="html">Delete</button>
        </div>
      </div>`;
    }

    const expanded = booksExpanded.has(s.name);
    const anyNeedsReconvert = s.volumes.some(v => v.needs_reconvert);
    const volumeRows = s.volumes.map(v => `
      <div class="reconvert-volume-row">
        <div class="reconvert-volume-header">
          <span class="reconvert-volume-name">${esc(v.name)}</span>
          ${v.modified_at ? `<span class="reconvert-volume-analyzed" title="Last analyzed">${formatAnalyzed(v.modified_at)}</span>` : ''}
          ${v.needs_reconvert ? `<span class="reconvert-needed-badge" title="Raw scan files on disk were modified after this volume's last conversion — Reconvert to pick up the change">⚠ raw scan changed</span>` : ''}
        </div>
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
          ${s.has_raw_scan ? `
            <select class="detector-size-picker" aria-label="OCR detail level" title="Text-detector resolution — higher catches more, but is slower">
              <option value="3072" selected>3072 (default, ~5.5GB GPU)</option>
              <option value="2048">2048 (faster, ~3GB GPU)</option>
              <option value="3584">Maximum (3584, ~6.3GB GPU, unverified)</option>
            </select>
            <button class="reconvert-btn" data-name="${esc(s.name)}" data-volume="${esc(v.name)}">Reconvert (full OCR)</button>
          ` : ''}
          <button class="job-delete-btn volume-delete-btn" data-name="${esc(s.name)}" data-volume="${esc(v.name)}">Delete</button>
        </div>
      </div>`).join('');
    return `
    <div class="library-card">
      <button class="book-expand-toggle" data-name="${esc(s.name)}" aria-expanded="${expanded}">
        <span class="book-expand-arrow">${expanded ? '▾' : '▸'}</span>
        <span class="library-card-name">${esc(s.name)}</span>
        ${anyNeedsReconvert ? `<span class="reconvert-needed-badge" title="One or more volumes' raw scan files were modified after their last conversion">⚠ raw scan changed</span>` : ''}
      </button>
      <div class="library-card-footer">
        <span class="library-card-count">${s.volumes.length} volume${s.volumes.length === 1 ? '' : 's'}</span>
        ${analyzedLabel}
        ${s.has_raw_scan
          ? `<select class="detector-size-picker" aria-label="OCR detail level" title="Text-detector resolution — higher catches more, but is slower">
              <option value="3072" selected>3072 (default, ~5.5GB GPU)</option>
              <option value="2048">2048 (faster, ~3GB GPU)</option>
              <option value="3584">Maximum (3584, ~6.3GB GPU, unverified)</option>
            </select>
            <button class="reconvert-btn" data-name="${esc(s.name)}">Reconvert all (full OCR)</button>`
          : `<span class="reconvert-no-scan" title="Raw scan no longer on disk — reconvert needs a fresh upload">no raw scan</span>`}
        <button class="reconvert-btn book-rename-btn" data-name="${esc(s.name)}">Rename</button>
        <button class="job-delete-btn book-delete-btn" data-name="${esc(s.name)}">Delete</button>
      </div>
      <p class="reconvert-error" style="color:#e07070;font-size:.78rem;margin:.35rem 0 0" hidden></p>
      ${volumeRows ? `<div class="reconvert-volumes" ${expanded ? '' : 'hidden'}>${volumeRows}</div>` : ''}
    </div>`;
  }).join('');
}

document.getElementById('books-list').addEventListener('click', async (e) => {
  const toggleBtn = e.target.closest('.book-expand-toggle');
  if (toggleBtn) {
    const name = toggleBtn.dataset.name;
    if (booksExpanded.has(name)) booksExpanded.delete(name);
    else booksExpanded.add(name);
    renderBooksList();
    return;
  }

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

  const renameBtn = e.target.closest('.book-rename-btn');
  if (renameBtn) {
    const name = renameBtn.dataset.name;
    const displayName = prompt('New display name (shown on the library page only — does not rename files):', name);
    if (!displayName || !displayName.trim() || displayName.trim() === name) return;
    renameBtn.disabled = true;
    renameBtn.textContent = 'Renaming…';
    try {
      const res = await fetch('/api/converter/books', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          library_id: selectedBooksLibraryId,
          name,
          kind: renameBtn.dataset.kind || '',
          display_name: displayName.trim(),
        }),
      });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        alert(d.error || 'Failed to rename');
        renameBtn.disabled = false;
        renameBtn.textContent = 'Rename';
        return;
      }
      renameBtn.textContent = 'Renamed';
      setTimeout(() => { renameBtn.textContent = 'Rename'; renameBtn.disabled = false; }, 1500);
    } catch {
      alert('Failed to rename');
      renameBtn.disabled = false;
      renameBtn.textContent = 'Rename';
    }
    return;
  }

  const delBtn = e.target.closest('.book-delete-btn, .volume-delete-btn');
  if (delBtn) {
    const name = delBtn.dataset.name;
    const volume = delBtn.dataset.volume || '';
    const confirmMsg = volume
      ? `Permanently delete volume "${volume}" from "${name}"? This removes just this EPUB and its own raw scan — cannot be undone.`
      : `Permanently delete "${name}"? This removes the EPUB(s) and the raw scan from disk — cannot be undone.`;
    if (!confirm(confirmMsg)) {
      return;
    }
    delBtn.disabled = true;
    delBtn.textContent = 'Deleting…';
    let url = `/api/converter/books?library=${encodeURIComponent(selectedBooksLibraryId)}&name=${encodeURIComponent(name)}`;
    if (volume) url += `&volume=${encodeURIComponent(volume)}`;
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
  const sizePicker = btn.parentElement.querySelector('.detector-size-picker');
  const detectorSize = sizePicker ? parseInt(sizePicker.value, 10) : 2048;
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
        detector_size: detectorSize,
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

// ── Server Status ────────────────────────────────────────────────────────────

function fmtBytes(n) {
  if (n == null) return '—';
  const gb = n / (1024 ** 3);
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(n / (1024 ** 2)).toFixed(0)} MB`;
}

function fmtPercent(n) {
  return n == null ? '—' : `${n.toFixed(0)}%`;
}

// The discrete GPU's own busy_percent sysfs file returns EINVAL (not just
// "0") while it's runtime-suspended between OCR jobs — a null reading here
// means "GPU asleep, nothing to report," not "feature broken," so it's
// labeled distinctly from a bare dash.
function fmtGPUBusy(n) {
  return n == null ? 'idle' : `${n.toFixed(0)}%`;
}

// Same EINVAL-while-suspended situation as gpu_busy_percent (see
// fmtGPUBusy) — the dGPU's own hwmon temp sensors are unreadable, not just
// zero, while it's runtime-suspended between OCR jobs.
function fmtGPUTemp(n) {
  return n == null ? 'idle' : `${n.toFixed(1)}°C`;
}

function fmtTemp(n) {
  return n == null ? '—' : `${n.toFixed(1)}°C`;
}

// Thresholds picked so a laptop under sustained heavy OCR load (this app's
// own normal operation) reads yellow, not red — red is reserved for genuinely
// close to the hardware's own crit/throttle point (100°C on this host's CPU
// and GPU alike), not just "busy."
function levelForPercent(pct) {
  if (pct == null) return '';
  if (pct >= 85) return 'status-danger';
  if (pct >= 60) return 'status-warn';
  return 'status-ok';
}
function levelForTemp(t) {
  if (t == null) return '';
  if (t >= 88) return 'status-danger';
  if (t >= 70) return 'status-warn';
  return 'status-ok';
}

let systemStatusIntervalId = null;

function startSystemStatusPolling() {
  const select = document.getElementById('status-refresh-interval');
  const refreshBtn = document.getElementById('status-refresh-now');
  if (!select) return;

  const apply = () => {
    if (systemStatusIntervalId) clearInterval(systemStatusIntervalId);
    systemStatusIntervalId = setInterval(loadSystemStatus, parseInt(select.value, 10));
  };
  select.addEventListener('change', apply);
  if (refreshBtn) refreshBtn.addEventListener('click', () => loadSystemStatus());
  apply();
}

async function loadSystemStatus() {
  const tiles = document.getElementById('status-tiles');
  if (!tiles) return;
  let data;
  try {
    data = await fetch('/api/system-status').then(r => r.json());
  } catch {
    tiles.innerHTML = '<p style="color:var(--text-dim)">Failed to load status.</p>';
    return;
  }
  const latest = data.latest || {};
  const history = data.history || [];

  const ramPct = latest.ram_total_bytes ? 100 * latest.ram_used_bytes / latest.ram_total_bytes : null;
  const vramPct = (latest.vram_total_bytes && latest.vram_used_bytes != null)
    ? 100 * latest.vram_used_bytes / latest.vram_total_bytes : null;

  tiles.innerHTML = `
    <div class="status-tile">
      <div class="status-tile-label">CPU load</div>
      <div class="status-tile-value ${levelForPercent(latest.cpu_percent)}">${fmtPercent(latest.cpu_percent)}</div>
    </div>
    <div class="status-tile">
      <div class="status-tile-label">CPU temp</div>
      <div class="status-tile-value ${levelForTemp(latest.cpu_temp_c)}">${fmtTemp(latest.cpu_temp_c)}</div>
    </div>
    <div class="status-tile">
      <div class="status-tile-label">RAM</div>
      <div class="status-tile-value ${levelForPercent(ramPct)}">${fmtBytes(latest.ram_used_bytes)} / ${fmtBytes(latest.ram_total_bytes)}</div>
      <div class="status-tile-sub">${fmtPercent(ramPct)}</div>
    </div>
    <div class="status-tile">
      <div class="status-tile-label">GPU load</div>
      <div class="status-tile-value ${levelForPercent(latest.gpu_busy_percent)}">${fmtGPUBusy(latest.gpu_busy_percent)}</div>
    </div>
    <div class="status-tile">
      <div class="status-tile-label">GPU temp</div>
      <div class="status-tile-value ${levelForTemp(latest.gpu_temp_c)}">${fmtGPUTemp(latest.gpu_temp_c)}</div>
    </div>
    <div class="status-tile">
      <div class="status-tile-label">VRAM</div>
      <div class="status-tile-value ${levelForPercent(vramPct)}">${fmtBytes(latest.vram_used_bytes)} / ${fmtBytes(latest.vram_total_bytes)}</div>
      <div class="status-tile-sub">${fmtPercent(vramPct)}</div>
    </div>
  `;

  drawTempGraph(history);
}

// Plain <canvas> line chart, no charting library — matches this app's
// no-new-dependencies convention (frontend has no build step at all).
function drawTempGraph(history) {
  const canvas = document.getElementById('status-temp-graph');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const w = canvas.width, h = canvas.height;
  ctx.clearRect(0, 0, w, h);

  const style = getComputedStyle(document.documentElement);
  const textColor = style.getPropertyValue('--text-dim').trim() || '#888';
  const borderColor = style.getPropertyValue('--border').trim() || '#333';

  if (history.length < 2) {
    ctx.fillStyle = textColor;
    ctx.font = '13px system-ui, sans-serif';
    ctx.fillText('Not enough data yet…', 12, h / 2);
    return;
  }

  const pad = { left: 36, right: 12, top: 12, bottom: 20 };
  const plotW = w - pad.left - pad.right;
  const plotH = h - pad.top - pad.bottom;

  const allTemps = [];
  for (const s of history) {
    if (s.cpu_temp_c != null) allTemps.push(s.cpu_temp_c);
    if (s.gpu_temp_c != null) allTemps.push(s.gpu_temp_c);
  }
  if (allTemps.length === 0) {
    ctx.fillStyle = textColor;
    ctx.font = '13px system-ui, sans-serif';
    ctx.fillText('No temperature sensors found.', 12, h / 2);
    return;
  }
  let minT = Math.floor(Math.min(...allTemps) / 10) * 10 - 5;
  let maxT = Math.ceil(Math.max(...allTemps) / 10) * 10 + 5;
  if (maxT - minT < 10) maxT = minT + 10;

  const t0 = new Date(history[0].time).getTime();
  const t1 = new Date(history[history.length - 1].time).getTime();
  const span = Math.max(1, t1 - t0);

  ctx.strokeStyle = borderColor;
  ctx.fillStyle = textColor;
  ctx.font = '11px system-ui, sans-serif';
  ctx.lineWidth = 1;
  const steps = 4;
  for (let i = 0; i <= steps; i++) {
    const temp = minT + (maxT - minT) * i / steps;
    const y = pad.top + plotH - (plotH * i / steps);
    ctx.beginPath();
    ctx.moveTo(pad.left, y);
    ctx.lineTo(w - pad.right, y);
    ctx.stroke();
    ctx.fillText(`${temp.toFixed(0)}°`, 4, y + 4);
  }

  function plotLine(key, color) {
    ctx.strokeStyle = color;
    ctx.lineWidth = 1.5;
    ctx.beginPath();
    let started = false;
    for (const s of history) {
      const v = s[key];
      if (v == null) { started = false; continue; }
      const x = pad.left + plotW * (new Date(s.time).getTime() - t0) / span;
      const y = pad.top + plotH - plotH * (v - minT) / (maxT - minT);
      if (!started) { ctx.moveTo(x, y); started = true; }
      else ctx.lineTo(x, y);
    }
    ctx.stroke();
  }

  plotLine('cpu_temp_c', '#e07070');
  plotLine('gpu_temp_c', '#6b8fd9');

  drawTimeAxis(ctx, t0, span, pad, plotW, h, textColor);
}

// X-axis clock-time labels, in UTC+3 regardless of the viewer's own timezone
// — this host's clock and the samples' own timestamps are UTC+3, and showing
// each viewer's local time would make the graph disagree with the server's
// actual wall-clock time (e.g. what hour a conversion job actually ran).
//
// Ticks snap to round clock times (:00, :15, :30, on-the-hour, ...) when the
// visible range actually contains one. A short window (a couple of minutes,
// e.g. right after a restart) very often contains *no* round boundary at
// all for any step up to 120min, which used to mean zero ticks and a blank
// axis — so if snapping produces fewer than 2 ticks, this falls back to
// plain evenly-spaced labels at the visible range's own start/mid/end
// instead, which are never round but are never empty either.
const GRAPH_TZ_OFFSET_MIN = 3 * 60;
const TICK_STEPS_MIN = [5, 10, 15, 30, 60, 120];

function hhmm(ms) {
  const d = new Date(ms);
  return `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')}`;
}

function drawTimeAxis(ctx, t0, span, pad, plotW, h, textColor) {
  const y = h - pad.bottom;
  ctx.fillStyle = textColor;
  ctx.font = '11px system-ui, sans-serif';
  ctx.textAlign = 'center';

  const shifted0 = t0 + GRAPH_TZ_OFFSET_MIN * 60000;
  const shifted1 = shifted0 + span;

  const minPxPerTick = 70;
  const maxTicks = Math.max(2, Math.floor(plotW / minPxPerTick));
  const spanMin = span / 60000;
  let stepMin = TICK_STEPS_MIN[TICK_STEPS_MIN.length - 1];
  for (const candidate of TICK_STEPS_MIN) {
    if (spanMin / candidate <= maxTicks) { stepMin = candidate; break; }
  }
  const stepMs = stepMin * 60000;

  const snapped = [];
  for (let tick = Math.ceil(shifted0 / stepMs) * stepMs; tick <= shifted1; tick += stepMs) {
    snapped.push(tick);
  }

  const ticks = snapped.length >= 2 ? snapped : [shifted0, (shifted0 + shifted1) / 2, shifted1];

  for (const tick of ticks) {
    const x = pad.left + plotW * (tick - shifted0) / span;
    ctx.fillText(hhmm(tick), x, y + 14);
  }
  ctx.textAlign = 'left';
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
