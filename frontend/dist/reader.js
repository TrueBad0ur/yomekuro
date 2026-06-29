'use strict';

const bookId = new URLSearchParams(location.search).get('id');
if (!bookId) location.href = '/';

let manifest = null;
let spineIndex = 0;
let isVertical = false;
let saveTimer = null;
let restoredProgression = 0;

const content = document.getElementById('reader-content');
const navTitle = document.getElementById('nav-title');
const navInfo = document.getElementById('nav-info');
const bottomInfo = document.getElementById('bottom-info');
const btnPrev = document.getElementById('btn-prev');
const btnNext = document.getElementById('btn-next');
const btnPrevB = document.getElementById('btn-prev-b');
const btnNextB = document.getElementById('btn-next-b');

// ── Init ──────────────────────────────────────────────────────────────────────

async function init() {
  let progress;
  [manifest, progress] = await Promise.all([
    fetch(`/api/books/${bookId}/manifest`).then(r => r.json()),
    fetch(`/api/books/${bookId}/progress`).then(r => r.json()),
  ]);

  document.title = manifest.title || 'yomekuro';
  navTitle.textContent = manifest.title || '';

  spineIndex = progress.spine_index || 0;
  restoredProgression = progress.progression || 0;

  await loadChapter(spineIndex, true);
}

// ── Load chapter ──────────────────────────────────────────────────────────────

async function loadChapter(index, restoreScroll) {
  if (!manifest || index < 0 || index >= manifest.spine.length) return;
  spineIndex = index;

  const item = manifest.spine[index];
  let text;
  try {
    const res = await fetch(`/api/books/${bookId}/content/${item.href}`);
    text = await res.text();
  } catch {
    content.innerHTML = '<p style="padding:2rem;color:#888">Failed to load chapter.</p>';
    return;
  }

  // Parse XHTML; fall back to HTML on parse error
  const parser = new DOMParser();
  let doc = parser.parseFromString(text, 'application/xhtml+xml');
  if (doc.querySelector('parsererror')) {
    doc = parser.parseFromString(text, 'text/html');
  }

  const chapterBase = item.href.includes('/')
    ? item.href.substring(0, item.href.lastIndexOf('/') + 1)
    : '';

  // Transfer html + body classes to reader container (writing-mode, etc.)
  const htmlClass = doc.documentElement ? doc.documentElement.className : '';
  const bodyClass = doc.body ? doc.body.className : '';
  content.className = ['reader-content', htmlClass, bodyClass].filter(Boolean).join(' ');
  isVertical = htmlClass.includes('vrtl') || htmlClass.includes('vertical');

  applyEpubStyles(doc, chapterBase);

  if (!doc.body) {
    content.innerHTML = '<p style="padding:2rem;color:#888">Empty chapter.</p>';
    updateNav();
    return;
  }

  rewriteNodes(doc.body, chapterBase);
  content.innerHTML = doc.body.innerHTML;

  updateNav();

  if (restoreScroll && restoredProgression > 0) {
    requestAnimationFrame(() => restorePosition(restoredProgression));
  } else {
    scrollToStart();
  }
}

// ── URL rewriting ─────────────────────────────────────────────────────────────

function resolveURL(chapterBase, rel) {
  if (!rel || rel.startsWith('data:') || rel.startsWith('http://') || rel.startsWith('https://')) {
    return rel;
  }
  if (rel.startsWith('#')) return rel; // fragment-only
  const base = new URL('http://x/' + chapterBase);
  const resolved = new URL(rel, base);
  // pathname starts with /
  const path = resolved.pathname.substring(1);
  return `/api/books/${bookId}/content/${path}`;
}

function rewriteNodes(root, chapterBase) {
  root.querySelectorAll('script').forEach(el => el.remove());

  root.querySelectorAll('img[src]').forEach(el => {
    el.setAttribute('src', resolveURL(chapterBase, el.getAttribute('src')));
  });
  root.querySelectorAll('image').forEach(el => {
    const href = el.getAttribute('href') || el.getAttribute('xlink:href');
    if (href) el.setAttribute('href', resolveURL(chapterBase, href));
  });
  root.querySelectorAll('source[src]').forEach(el => {
    el.setAttribute('src', resolveURL(chapterBase, el.getAttribute('src')));
  });
  // Inline style background-image (simple cases)
  root.querySelectorAll('[style]').forEach(el => {
    el.setAttribute('style',
      el.getAttribute('style').replace(
        /url\(['"]?([^'")]+)['"]?\)/g,
        (_, u) => `url(${resolveURL(chapterBase, u)})`
      )
    );
  });
}

function applyEpubStyles(doc, chapterBase) {
  document.querySelectorAll('link.epub-style').forEach(el => el.remove());
  doc.querySelectorAll('link[rel="stylesheet"]').forEach(link => {
    const href = link.getAttribute('href');
    if (!href) return;
    const el = document.createElement('link');
    el.rel = 'stylesheet';
    el.className = 'epub-style';
    el.href = resolveURL(chapterBase, href);
    document.head.appendChild(el);
  });
}

// ── Scroll / progress ─────────────────────────────────────────────────────────

function scrollToStart() {
  if (isVertical) {
    // RTL vertical: start at the right edge
    requestAnimationFrame(() => { content.scrollLeft = content.scrollWidth; });
  } else {
    window.scrollTo(0, 0);
  }
}

function restorePosition(progression) {
  if (isVertical) {
    const max = content.scrollWidth - content.clientWidth;
    content.scrollLeft = max * (1 - progression); // RTL: 0 progression = right edge
  } else {
    const max = document.documentElement.scrollHeight - window.innerHeight;
    window.scrollTo(0, max * progression);
  }
}

function getProgression() {
  if (isVertical) {
    const max = content.scrollWidth - content.clientWidth;
    if (max <= 0) return 0;
    // RTL: scrollLeft=0 means end of text; scrollLeft=max means start
    return 1 - (content.scrollLeft / max);
  }
  const max = document.documentElement.scrollHeight - window.innerHeight;
  if (max <= 0) return 0;
  return window.scrollY / max;
}

function saveProgress() {
  if (!manifest) return;
  const progression = getProgression();
  const percentage = (spineIndex + progression) / manifest.spine.length;
  fetch(`/api/books/${bookId}/progress`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      spine_index: spineIndex,
      progression: Math.max(0, Math.min(1, progression)),
      percentage: Math.max(0, Math.min(1, percentage)),
    }),
  }).catch(() => {});
}

window.addEventListener('scroll', () => {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveProgress, 800);
});
content.addEventListener('scroll', () => {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveProgress, 800);
});

// ── Navigation ────────────────────────────────────────────────────────────────

function updateNav() {
  if (!manifest) return;
  const total = manifest.spine.length;
  const info = `${spineIndex + 1} / ${total}`;
  navInfo.textContent = info;
  bottomInfo.textContent = info;

  btnPrev.disabled = btnPrevB.disabled = spineIndex <= 0;
  btnNext.disabled = btnNextB.disabled = spineIndex >= total - 1;
}

async function goPrev() {
  saveProgress();
  await loadChapter(spineIndex - 1, false);
}

async function goNext() {
  saveProgress();
  await loadChapter(spineIndex + 1, false);
}

btnPrev.addEventListener('click', goPrev);
btnNext.addEventListener('click', goNext);
btnPrevB.addEventListener('click', goPrev);
btnNextB.addEventListener('click', goNext);

document.addEventListener('keydown', e => {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;
  if (e.key === 'ArrowRight' || e.key === 'PageDown') {
    if (!btnNext.disabled) goNext();
  } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
    if (!btnPrev.disabled) goPrev();
  }
});

// ── Start ─────────────────────────────────────────────────────────────────────

init().catch(err => {
  content.innerHTML = `<p style="padding:2rem;color:#c77">Error: ${err.message}</p>`;
});
