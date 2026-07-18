'use strict';

const bookId = new URLSearchParams(location.search).get('id');
if (!bookId) location.href = '/';

// Shared with app.js's toggle (same localStorage key) — 'off' cache-busts every
// content/image URL instead of relying on the server's max-age, e.g. right
// after a reconvert when you want the new pages, not whatever's cached.
function getCacheMode() {
  return localStorage.getItem('cacheMode') || 'on';
}
function cacheBust(url) {
  if (getCacheMode() === 'off') {
    return url + (url.includes('?') ? '&' : '?') + '_nc=' + Date.now();
  }
  return url;
}

// ── State ─────────────────────────────────────────────────────────────────────

let manifest = null;
let spineIndex = 0;
let isVertical = false;
let isFixedLayout = false;
let saveTimer = null;
let restoredProgression = 0;
let bookmarkSpine = null;
let bookmarkElem  = null;
let bookmarkStart = null;
let bookmarkEnd   = null;
let fixedDoc = null;
let fixedChapterBase = '';
let fixedDoc2 = null;
let fixedChapterBase2 = '';
let spreadMode = false;
let zoomLevel = 1.0;
let fusedHalf = 0; // which physical half (0=left crop, 1=right crop) of a fused-spread page is shown
let pendingFusedHalf = null; // goPrev/goNext set this right before loadChapter to land on a specific half
const ZOOM_STEP = 1.3;
const ZOOM_MAX  = 6.0;

const content    = document.getElementById('reader-content');
const navTitle   = document.getElementById('nav-title');
const navInfo    = document.getElementById('nav-info');
const bottomInfo = document.getElementById('bottom-info');
const btnPrev    = document.getElementById('btn-prev');
const btnNext    = document.getElementById('btn-next');
const btnPrevB   = document.getElementById('btn-prev-b');
const btnNextB   = document.getElementById('btn-next-b');
const btnTOC     = document.getElementById('btn-toc');
const btnSpread  = document.getElementById('btn-spread');
const tocPanel   = document.getElementById('toc-panel');
const tocOverlay = document.getElementById('toc-overlay');
const tocClose   = document.getElementById('toc-close');
const tocList    = document.getElementById('toc-list');

function openTOC()  { tocPanel.classList.add('open');  tocOverlay.classList.add('visible'); }
function closeTOC() { tocPanel.classList.remove('open'); tocOverlay.classList.remove('visible'); }

btnTOC.addEventListener('click', openTOC);
tocClose.addEventListener('click', closeTOC);
tocOverlay.addEventListener('click', closeTOC);

// A two-page spread only makes sense on a device with a mouse/trackpad-sized
// screen — never on a phone, however spreadMode is set. Detected by pointer
// type, not viewport width: width alone flips in landscape (very much how
// manga gets read), where a phone's width easily exceeds any fixed px cutoff.
function isMobileViewport() { return window.matchMedia('(pointer: coarse)').matches; }
function effectiveSpread() { return spreadMode && !isMobileViewport(); }

btnSpread.addEventListener('click', async () => {
  if (isMobileViewport()) return;
  spreadMode = !spreadMode;
  btnSpread.classList.toggle('active', spreadMode);
  await loadChapter(spineIndex, false);
});

function renderTOC(entries, level) {
  for (const e of entries) {
    const btn = document.createElement('button');
    btn.className = 'toc-item';
    btn.style.paddingLeft = `${0.75 + level * 1}rem`;
    btn.textContent = e.label;
    if (e.spine_index < 0) {
      btn.disabled = true;
    } else {
      btn.addEventListener('click', () => {
        saveProgress();
        loadChapter(e.spine_index, false);
        closeTOC();
      });
    }
    tocList.appendChild(btn);
    if (e.children && e.children.length > 0) {
      renderTOC(e.children, level + 1);
    }
  }
}

// ── Bookmark ──────────────────────────────────────────────────────────────────

function bookmarkElements() {
  return Array.from(content.querySelectorAll('p, li, h1, h2, h3, h4, h5, h6, blockquote'));
}

// Character offset of targetNode:targetOffset within container's text nodes
function getTextOffset(container, targetNode, targetOffset) {
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  let total = 0, node;
  while ((node = walker.nextNode())) {
    if (node === targetNode) return total + targetOffset;
    total += node.textContent.length;
  }
  return total + targetOffset;
}

// DOM position (node + offset) from a plain-text character offset within container
function domPositionFromOffset(container, offset) {
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  let remaining = offset, node, last;
  while ((node = walker.nextNode())) {
    last = node;
    if (remaining <= node.textContent.length) return { node, offset: remaining };
    remaining -= node.textContent.length;
  }
  return last ? { node: last, offset: last.textContent.length } : null;
}

function applyBookmarkMark() {
  // Unwrap any existing .reading-mark elements
  content.querySelectorAll('.reading-mark').forEach(mark => {
    const parent = mark.parentNode;
    while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
    parent.removeChild(mark);
  });

  if (bookmarkSpine !== spineIndex || bookmarkElem === null || bookmarkStart === null) return;

  const elems = bookmarkElements();
  const target = elems[bookmarkElem];
  if (!target) return;

  try {
    const s = domPositionFromOffset(target, bookmarkStart);
    const e = domPositionFromOffset(target, bookmarkEnd);
    if (!s || !e) return;

    const range = document.createRange();
    range.setStart(s.node, s.offset);
    range.setEnd(e.node, e.offset);

    markTextNodesInRange(range, target);
  } catch {
    // ignore if DOM has changed in unexpected ways
  }
}

// Wraps each text-node fragment inside `range` in its own <mark>. Never moves
// element nodes: that would rip <rt> out of <ruby> and break the furigana.
function markTextNodesInRange(range, root) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: node => range.intersectsNode(node) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT,
  });
  const textNodes = [];
  let node;
  while ((node = walker.nextNode())) textNodes.push(node);

  for (const textNode of textNodes) {
    const start = textNode === range.startContainer ? range.startOffset : 0;
    const end   = textNode === range.endContainer ? range.endOffset : textNode.length;
    if (start >= end) continue;

    const subRange = document.createRange();
    subRange.setStart(textNode, start);
    subRange.setEnd(textNode, end);

    const mark = document.createElement('mark');
    mark.className = 'reading-mark';
    subRange.surroundContents(mark); // range spans a single text node: always safe
  }
}

// Popup for removing bookmark when clicking on highlighted text
let bmPopup = null;

function hideBmPopup() {
  if (bmPopup) { bmPopup.remove(); bmPopup = null; }
}

function showBmPopup(x, y) {
  bmPopup = document.createElement('div');
  bmPopup.className = 'bm-popup';
  const btn = document.createElement('button');
  btn.textContent = 'Remove bookmark';
  bmPopup.appendChild(btn);
  // keep popup inside viewport
  const px = Math.min(x, window.innerWidth - 180);
  const py = y + 10;
  bmPopup.style.left = px + 'px';
  bmPopup.style.top  = py + 'px';
  document.body.appendChild(bmPopup);

  btn.addEventListener('click', (e) => {
    e.stopPropagation();
    bookmarkSpine = null;
    bookmarkElem  = null;
    bookmarkStart = null;
    bookmarkEnd   = null;
    applyBookmarkMark();
    saveProgress();
    hideBmPopup();
  });

  bmPopup.addEventListener('click', e => e.stopPropagation());
}

// Text selection → bookmark (desktop: any mouse selection; touch: gated below)
document.addEventListener('mouseup', onSelectionEnd);

function onSelectionEnd() {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return;
  const range = sel.getRangeAt(0);
  if (!content.contains(range.commonAncestorContainer)) return;

  const elems = bookmarkElements();
  let elemIndex = -1, elemNode = null;
  for (let i = 0; i < elems.length; i++) {
    if (elems[i].contains(range.startContainer)) {
      elemIndex = i; elemNode = elems[i]; break;
    }
  }
  if (elemIndex < 0) return;

  const start = getTextOffset(elemNode, range.startContainer, range.startOffset);
  const end   = getTextOffset(elemNode, range.endContainer,   range.endOffset);
  if (start === end) return;

  sel.removeAllRanges();
  bookmarkSpine = spineIndex;
  bookmarkElem  = elemIndex;
  bookmarkStart = start;
  bookmarkEnd   = end;
  applyBookmarkMark();
  saveProgress();
}

// Click on highlighted text → show Remove popup
content.addEventListener('click', (e) => {
  const mark = e.target.closest('.reading-mark');
  hideBmPopup();
  if (mark) {
    e.stopPropagation();
    showBmPopup(e.clientX, e.clientY);
  }
});

document.addEventListener('click', hideBmPopup);

// ── Touch gestures: long-press to select, swipe/tap-zone to page ───────────────
// A bare tap must never select text — only a genuine long-press does. Swipe and
// tap-zones give fixed-layout (manga) a way to turn pages beyond the tiny nav buttons.

const LONG_PRESS_MS = 500;
const TAP_MOVE_TOLERANCE = 10;
const SWIPE_THRESHOLD = 60;

let touchStartX = 0, touchStartY = 0, touchStartTime = 0, touchMoved = false;

function isInteractiveTarget(target) {
  return !!target.closest(
    'button, a, input, textarea, .reader-nav, .reader-bottom-nav, .toc-panel, ' +
    '.toc-overlay, .book-menu-popup, .bm-popup, .tag-editor-popup, mark.reading-mark'
  );
}

function handleSwipe(dx, target) {
  if (!isFixedLayout || isInteractiveTarget(target)) return;
  const rtl = manifest && manifest.reading_direction === 'rtl';
  if (dx < 0) { rtl ? goPrev() : goNext(); } else { rtl ? goNext() : goPrev(); }
}

function handleTap(x, target) {
  if (!isFixedLayout || isInteractiveTarget(target)) return;
  const w = window.innerWidth;
  const rtl = manifest && manifest.reading_direction === 'rtl';
  if (x < w * 0.3) { rtl ? goNext() : goPrev(); }
  else if (x > w * 0.7) { rtl ? goPrev() : goNext(); }
}

document.addEventListener('touchstart', (e) => {
  if (e.touches.length !== 1) return;
  touchStartX = e.touches[0].clientX;
  touchStartY = e.touches[0].clientY;
  touchStartTime = Date.now();
  touchMoved = false;
}, { passive: true });

document.addEventListener('touchmove', (e) => {
  if (e.touches.length !== 1) return;
  const dx = e.touches[0].clientX - touchStartX;
  const dy = e.touches[0].clientY - touchStartY;
  if (Math.hypot(dx, dy) > TAP_MOVE_TOLERANCE) touchMoved = true;
}, { passive: true });

document.addEventListener('touchend', (e) => {
  const elapsed = Date.now() - touchStartTime;
  const touch = e.changedTouches[0];
  const endX = touch ? touch.clientX : touchStartX;
  const endY = touch ? touch.clientY : touchStartY;
  const dx = endX - touchStartX;
  const dy = endY - touchStartY;

  if (!touchMoved && elapsed >= LONG_PRESS_MS) {
    onSelectionEnd();
    return;
  }

  // Not a long-press: don't let an incidental selection turn into a bookmark.
  const sel = window.getSelection();
  if (sel && !sel.isCollapsed) sel.removeAllRanges();

  if (touchMoved && elapsed < 600 && Math.abs(dx) > SWIPE_THRESHOLD && Math.abs(dx) > Math.abs(dy) * 1.5) {
    handleSwipe(dx, e.target);
  } else if (!touchMoved && elapsed < LONG_PRESS_MS) {
    handleTap(endX, e.target);
  }
});

// ── Init ──────────────────────────────────────────────────────────────────────

async function init() {
  let progress;
  [manifest, progress] = await Promise.all([
    fetch(`/api/books/${bookId}/manifest`).then(r => r.json()),
    fetch(`/api/books/${bookId}/progress`).then(r => r.json()),
  ]);

  document.title = manifest.title || 'yomekuro';
  navTitle.textContent = manifest.title || '';
  isFixedLayout = !!manifest.fixed_layout;

  if (isFixedLayout) {
    document.body.classList.add('fixed-layout-mode');
    btnSpread.style.display = '';
  }

  spineIndex = progress.spine_index || 0;
  restoredProgression = progress.progression || 0;
  bookmarkSpine = progress.bookmark_spine ?? null;
  bookmarkElem  = progress.bookmark_elem  ?? null;
  bookmarkStart = progress.bookmark_start ?? null;
  bookmarkEnd   = progress.bookmark_end   ?? null;

  if (manifest.toc && manifest.toc.length > 0) {
    renderTOC(manifest.toc, 0);
    btnTOC.disabled = false;
  } else {
    btnTOC.disabled = true;
  }

  await loadChapter(spineIndex, true);
}

// ── Load chapter ──────────────────────────────────────────────────────────────

async function loadChapter(index, restoreScroll) {
  if (!manifest || index < 0 || index >= manifest.spine.length) return;
  spineIndex = index;

  const item = manifest.spine[index];
  let text;
  try {
    const res = await fetch(cacheBust(`/api/books/${bookId}/content/${item.href}`));
    text = await res.text();
  } catch {
    content.innerHTML = '<p style="padding:2rem;color:#888">Failed to load chapter.</p>';
    return;
  }

  const parser = new DOMParser();
  let doc = parser.parseFromString(text, 'application/xhtml+xml');
  if (doc.querySelector('parsererror')) {
    doc = parser.parseFromString(text, 'text/html');
  }

  const chapterBase = item.href.includes('/')
    ? item.href.substring(0, item.href.lastIndexOf('/') + 1)
    : '';

  if (!doc.body) {
    content.innerHTML = '<p style="padding:2rem;color:#888">Empty chapter.</p>';
    updateNav();
    return;
  }

  if (isFixedLayout) {
    fixedDoc = doc;
    fixedChapterBase = chapterBase;
    fixedDoc2 = null;
    fixedChapterBase2 = '';
    content.className = 'reader-content';
    fusedHalf = pendingFusedHalf !== null ? pendingFusedHalf : firstHalfInReadingOrder();
    pendingFusedHalf = null;

    if (effectiveSpread() && !isFusedSpreadPage(fixedDoc) && spineIndex + 1 < manifest.spine.length) {
      const item2 = manifest.spine[spineIndex + 1];
      try {
        const res2 = await fetch(cacheBust(`/api/books/${bookId}/content/${item2.href}`));
        const text2 = await res2.text();
        const parser2 = new DOMParser();
        let doc2 = parser2.parseFromString(text2, 'application/xhtml+xml');
        if (doc2.querySelector('parsererror')) doc2 = parser2.parseFromString(text2, 'text/html');
        fixedDoc2 = doc2;
        fixedChapterBase2 = item2.href.includes('/')
          ? item2.href.substring(0, item2.href.lastIndexOf('/') + 1) : '';
      } catch { /* fixedDoc2 stays null */ }
    }

    renderCurrentFixed();
    updateNav();
    return;
  }

  const htmlClass = doc.documentElement ? doc.documentElement.className : '';
  const bodyClass = doc.body.className;
  content.className = ['reader-content', htmlClass, bodyClass].filter(Boolean).join(' ');
  isVertical = htmlClass.includes('vrtl') || htmlClass.includes('vertical');

  applyEpubStyles(doc, chapterBase);
  rewriteNodes(doc.body, chapterBase);
  content.innerHTML = doc.body.innerHTML;

  if (manifest.format === 'html') matchChromeToContent();

  updateNav();

  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (restoreScroll && restoredProgression > 0) {
      restorePosition(restoredProgression);
    } else {
      scrollToStart();
    }
    applyBookmarkMark();
  }));
}

// ── URL rewriting ─────────────────────────────────────────────────────────────

function resolveURL(chapterBase, rel) {
  if (!rel || rel.startsWith('data:') || rel.startsWith('http://') || rel.startsWith('https://')) {
    return rel;
  }
  if (rel.startsWith('#')) return rel;
  const base = new URL('http://x/' + chapterBase);
  const resolved = new URL(rel, base);
  return `/api/books/${bookId}/content/${resolved.pathname.substring(1)}`;
}

function rewriteNodes(root, chapterBase) {
  root.querySelectorAll('script').forEach(el => el.remove());
  root.querySelectorAll('img[src]').forEach(el => {
    el.setAttribute('src', cacheBust(resolveURL(chapterBase, el.getAttribute('src'))));
  });
  root.querySelectorAll('image').forEach(el => {
    const href = el.getAttribute('href') || el.getAttribute('xlink:href');
    if (href) el.setAttribute('href', cacheBust(resolveURL(chapterBase, href)));
  });
  root.querySelectorAll('source[src]').forEach(el => {
    el.setAttribute('src', cacheBust(resolveURL(chapterBase, el.getAttribute('src'))));
  });
  root.querySelectorAll('[style]').forEach(el => {
    el.setAttribute('style',
      el.getAttribute('style').replace(
        /url\(['"]?([^'")]+)['"]?\)/g,
        (_, u) => `url(${cacheBust(resolveURL(chapterBase, u))})`
      )
    );
  });
}

function applyEpubStyles(doc, chapterBase) {
  document.querySelectorAll('.epub-style').forEach(el => el.remove());
  doc.querySelectorAll('link[rel="stylesheet"]').forEach(link => {
    const href = link.getAttribute('href');
    if (!href) return;
    const el = document.createElement('link');
    el.rel = 'stylesheet';
    el.className = 'epub-style';
    el.href = cacheBust(resolveURL(chapterBase, href));
    document.head.appendChild(el);
  });
  doc.querySelectorAll('style').forEach(style => {
    const el = document.createElement('style');
    el.className = 'epub-style';
    el.textContent = style.textContent;
    document.head.appendChild(el);
  });
}

// HTML books bring unscoped CSS that clashes with the chrome bars, so drop the
// bars' background and pin them to the light palette — the source isn't themed.
function matchChromeToContent() {
  document.body.classList.add('html-reader-chrome');
  document.querySelectorAll('.reader-nav, .reader-bottom-nav').forEach(el => {
    el.style.backgroundColor = 'transparent';
    el.style.borderColor = 'transparent';
  });
}

// ── Fixed-layout rendering ────────────────────────────────────────────────────

function parsePx(styleStr, prop) {
  const m = styleStr ? styleStr.match(new RegExp(prop + '\\s*:\\s*(\\d+)px')) : null;
  return m ? parseInt(m[1], 10) : 0;
}

// Some scans are pre-fused two-page spreads (both facing pages photographed as
// one wide image) rather than one image per physical page — width/height alone
// tells them apart from a normal portrait page. Spread mode must never combine
// one of these with a neighbour: that would show two spreads (four pages) at
// once instead of the one spread the image already is.
const SPREAD_ASPECT_THRESHOLD = 1.15;

function isFusedSpreadPage(doc) {
  const bodyDiv = doc && doc.body ? doc.body.firstElementChild : null;
  const styleStr = bodyDiv ? (bodyDiv.getAttribute('style') || '') : '';
  const pw = parsePx(styleStr, 'width');
  const ph = parsePx(styleStr, 'height');
  return pw > 0 && ph > 0 && (pw / ph) >= SPREAD_ASPECT_THRESHOLD;
}

// Whether the current page renders combined with a neighbour — false whenever
// spread mode is off, or the current page is already a fused spread.
function isRenderingSpread() {
  return effectiveSpread() && isFixedLayout && fixedDoc && !isFusedSpreadPage(fixedDoc);
}

// A fused-spread page shows as one cropped half by default (single-page mode),
// and only as the full original image once Spread is turned on — Spread mode
// on such a page means "show the spread that's already there", not "combine
// with a neighbour".
function shouldCropHalf(doc) {
  return isFusedSpreadPage(doc) && !effectiveSpread();
}

// Which physical half (0=left, 1=right) is read first — the right one for RTL
// manga, since the right page comes first in reading order.
function firstHalfInReadingOrder() {
  return (manifest && manifest.reading_direction === 'rtl') ? 1 : 0;
}

function renderCurrentFixed() {
  if (!fixedDoc) return;
  if (isRenderingSpread() && fixedDoc2) {
    renderFixedSpread(fixedDoc, fixedChapterBase, fixedDoc2, fixedChapterBase2);
  } else {
    renderFixedPage(fixedDoc, fixedChapterBase);
  }
}

// halfIndex null renders the page in full; 0/1 clips to just that half (left/
// right), keeping the inner content at its real pw×ph size so the OCR overlay
// divs' absolute px positions (measured against the full image) stay correct —
// only the wrapper's width and an inner offset change, nothing about the text
// layer's own coordinates.
function buildPageWrapper(bodyDiv, chapterBase, pw, ph, totalScale, left, halfIndex) {
  const half = halfIndex != null;
  const outerW = half ? pw / 2 : pw;

  const wrapper = document.createElement('div');
  wrapper.style.cssText = [
    'position:absolute', 'top:0', `left:${left}px`,
    `width:${outerW}px`, `height:${ph}px`,
    `transform:scale(${totalScale})`, 'transform-origin:top left',
    'overflow:hidden',
  ].join(';');

  let inner = wrapper;
  if (half) {
    inner = document.createElement('div');
    inner.style.cssText = [
      'position:absolute', 'top:0', `left:${-halfIndex * (pw / 2)}px`,
      `width:${pw}px`, `height:${ph}px`,
    ].join(';');
    wrapper.appendChild(inner);
  }

  for (const child of Array.from(bodyDiv.children)) {
    const tag = child.nodeName.toLowerCase();
    if (tag === 'img') {
      const img = document.createElement('img');
      img.style.cssText = 'position:absolute;top:0;left:0;width:100%;height:100%;display:block;';
      img.src = cacheBust(resolveURL(chapterBase, child.getAttribute('src') || ''));
      img.alt = child.getAttribute('alt') || '';
      inner.appendChild(img);
    } else {
      const overlay = document.createElement('div');
      overlay.style.cssText = child.getAttribute('style') || '';
      overlay.textContent = child.textContent || '';
      inner.appendChild(overlay);
    }
  }
  return wrapper;
}

function renderFixedSpread(doc1, base1, doc2, base2) {
  content.innerHTML = '';
  const bodyDiv1 = doc1.body ? doc1.body.firstElementChild : null;
  const bodyDiv2 = doc2 && doc2.body ? doc2.body.firstElementChild : null;
  if (!bodyDiv1) {
    content.innerHTML = '<p style="padding:2rem;color:#888">Could not render page.</p>';
    return;
  }
  const styleStr = bodyDiv1.getAttribute('style') || '';
  const pw = parsePx(styleStr, 'width')  || 1350;
  const ph = parsePx(styleStr, 'height') || 1920;

  const navEl = document.querySelector('.reader-nav');
  const navH  = navEl ? navEl.offsetHeight : 56;
  const vw = window.innerWidth;
  const vh = window.innerHeight - navH;

  const totalPW    = bodyDiv2 ? pw * 2 : pw;
  const baseScale  = Math.min(vw / totalPW, vh / ph);
  const totalScale = baseScale * zoomLevel;
  const scaledW    = Math.ceil(pw * totalScale);
  const scaledH    = Math.ceil(ph * totalScale);
  const scaledTotalW = Math.ceil(totalPW * totalScale);

  // position:fixed removes from flex flow → reliable height, scroll always works
  content.style.cssText = `position:fixed;top:${navH}px;left:0;right:0;bottom:0;overflow:auto;background:#000;`;

  const scrollerW = Math.max(scaledTotalW, vw);
  const scroller  = document.createElement('div');
  scroller.style.cssText = `position:relative;width:${scrollerW}px;height:${scaledH}px;`;

  const leftPad = Math.max(0, Math.floor((scrollerW - scaledTotalW) / 2));
  const isRTL = manifest && manifest.reading_direction === 'rtl';

  // RTL: current page (lower spine index) on right, next on left
  // LTR: current page on left, next on right
  const left1 = isRTL && bodyDiv2 ? leftPad + scaledW : leftPad;
  const left2 = isRTL ? leftPad : leftPad + scaledW;

  scroller.appendChild(buildPageWrapper(bodyDiv1, base1, pw, ph, totalScale, left1));
  if (bodyDiv2) {
    scroller.appendChild(buildPageWrapper(bodyDiv2, base2, pw, ph, totalScale, left2));
  }
  content.appendChild(scroller);
  content.scrollTop = 0;
  content.scrollLeft = 0;
}

function renderFixedPage(doc, chapterBase) {
  content.innerHTML = '';

  const bodyDiv = doc.body ? doc.body.firstElementChild : null;
  if (!bodyDiv) {
    content.innerHTML = '<p style="padding:2rem;color:#888">Could not render page.</p>';
    return;
  }

  const styleStr = bodyDiv.getAttribute('style') || '';
  const pw = parsePx(styleStr, 'width')  || 1350;
  const ph = parsePx(styleStr, 'height') || 1920;

  const crop = shouldCropHalf(doc);
  const halfIndex = crop ? fusedHalf : null;
  const effectivePW = crop ? pw / 2 : pw;

  const navEl = document.querySelector('.reader-nav');
  const navH  = navEl ? navEl.offsetHeight : 56;
  const vw = window.innerWidth;
  const vh = window.innerHeight - navH;

  const baseScale  = Math.min(vw / effectivePW, vh / ph);
  const totalScale = baseScale * zoomLevel;
  const scaledW    = Math.ceil(effectivePW * totalScale);
  const scaledH    = Math.ceil(ph * totalScale);

  content.style.cssText = `position:fixed;top:${navH}px;left:0;right:0;bottom:0;overflow:auto;background:#000;`;

  const scrollerW = Math.max(scaledW, vw);
  const scroller  = document.createElement('div');
  scroller.style.cssText = `position:relative;width:${scrollerW}px;height:${scaledH}px;`;

  const leftPad = Math.max(0, Math.floor((scrollerW - scaledW) / 2));
  scroller.appendChild(buildPageWrapper(bodyDiv, chapterBase, pw, ph, totalScale, leftPad, halfIndex));
  content.appendChild(scroller);
  content.scrollTop  = 0;
  content.scrollLeft = 0;
}

window.addEventListener('resize', () => {
  if (isFixedLayout && fixedDoc) renderCurrentFixed();
});

// ── Scroll / progress ─────────────────────────────────────────────────────────

function scrollToStart() {
  if (isVertical) {
    content.scrollLeft = content.scrollWidth;
  } else {
    window.scrollTo(0, 0);
    document.documentElement.scrollTop = 0;
    document.body.scrollTop = 0;
  }
}

function restorePosition(progression) {
  if (isVertical) {
    const max = content.scrollWidth - content.clientWidth;
    content.scrollLeft = max * (1 - progression);
  } else {
    const max = document.documentElement.scrollHeight - window.innerHeight;
    window.scrollTo(0, max * progression);
  }
}

function getProgression() {
  if (isFixedLayout) return 0; // no intra-page progression for manga
  if (isVertical) {
    const max = content.scrollWidth - content.clientWidth;
    if (max <= 0) return 0;
    return 1 - (content.scrollLeft / max);
  }
  const max = document.documentElement.scrollHeight - window.innerHeight;
  if (max <= 0) return 0;
  return window.scrollY / max;
}

function saveProgress() {
  if (!manifest) return;
  const progression = getProgression();
  // Fixed-layout has no intra-page progression: on page N, N pages are read.
  // Counting (N-1)+0 would cap a finished manga at 99% and undo "mark as read".
  const percentage = isFixedLayout
    ? (spineIndex + 1) / manifest.spine.length
    : (spineIndex + progression) / manifest.spine.length;
  fetch(`/api/books/${bookId}/progress`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      spine_index:    spineIndex,
      progression:    Math.max(0, Math.min(1, progression)),
      percentage:     Math.max(0, Math.min(1, percentage)),
      bookmark_spine: bookmarkSpine,
      bookmark_elem:  bookmarkElem,
      bookmark_start: bookmarkStart,
      bookmark_end:   bookmarkEnd,
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
  const info  = `${spineIndex + 1} / ${total}`;
  navInfo.textContent  = info;
  bottomInfo.textContent = info;
  // At the book's edge, prev/next is only a true no-op once any fused-page half
  // stepping is exhausted too — otherwise there's still a half left to show.
  const onFirstHalf = !onFusedHalf() || fusedHalf === firstHalfInReadingOrder();
  const onLastHalf  = !onFusedHalf() || fusedHalf === 1 - firstHalfInReadingOrder();
  btnPrev.disabled  = btnPrevB.disabled  = spineIndex <= 0 && onFirstHalf;
  btnNext.disabled  = btnNextB.disabled  = spineIndex >= total - 1 && onLastHalf;
}

// A fused-spread page is two logical pages in single-page mode: step within
// its two halves before actually moving the spine index.
function onFusedHalf() {
  return isFixedLayout && !effectiveSpread() && fixedDoc && isFusedSpreadPage(fixedDoc);
}

async function goPrev() {
  if (onFusedHalf() && fusedHalf !== firstHalfInReadingOrder()) {
    saveProgress();
    fusedHalf = firstHalfInReadingOrder();
    renderCurrentFixed();
    updateNav();
    return;
  }
  saveProgress();
  const step = isRenderingSpread() ? 2 : 1;
  pendingFusedHalf = 1 - firstHalfInReadingOrder(); // enter the previous page from its far end
  await loadChapter(Math.max(0, spineIndex - step), false);
}
async function goNext() {
  if (onFusedHalf() && fusedHalf !== 1 - firstHalfInReadingOrder()) {
    saveProgress();
    fusedHalf = 1 - firstHalfInReadingOrder();
    renderCurrentFixed();
    updateNav();
    return;
  }
  saveProgress();
  const step = isRenderingSpread() ? 2 : 1;
  pendingFusedHalf = firstHalfInReadingOrder();
  await loadChapter(Math.min(manifest.spine.length - 1, spineIndex + step), false);
}

btnPrev.addEventListener('click', goPrev);
btnNext.addEventListener('click', goNext);
btnPrevB.addEventListener('click', goPrev);
btnNextB.addEventListener('click', goNext);

document.addEventListener('keydown', e => {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;

  // Ctrl+zoom for fixed-layout manga
  if (isFixedLayout && fixedDoc && (e.ctrlKey || e.metaKey)) {
    if (e.key === '=' || e.key === '+') {
      e.preventDefault();
      zoomLevel = Math.min(zoomLevel * ZOOM_STEP, ZOOM_MAX);
      renderCurrentFixed();
      return;
    }
    if (e.key === '-' || e.key === '_') {
      e.preventDefault();
      zoomLevel = Math.max(zoomLevel / ZOOM_STEP, 1.0);
      renderCurrentFixed();
      return;
    }
    if (e.key === '0') {
      e.preventDefault();
      zoomLevel = 1.0;
      renderCurrentFixed();
      return;
    }
  }

  // Scroll content div in fixed-layout mode
  if (isFixedLayout && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    e.preventDefault();
    content.scrollTop += e.key === 'ArrowDown' ? 150 : -150;
    return;
  }

  // RTL manga: left arrow = next page (forward in reading order), right = prev
  const rtl = isFixedLayout && manifest && manifest.reading_direction === 'rtl';
  const nextKey = rtl ? 'ArrowLeft'  : 'ArrowRight';
  const prevKey = rtl ? 'ArrowRight' : 'ArrowLeft';
  if ((e.key === nextKey || e.key === 'PageDown') && !btnNext.disabled) goNext();
  if ((e.key === prevKey || e.key === 'PageUp')   && !btnPrev.disabled) goPrev();
});

// ── Scroll-hide nav ───────────────────────────────────────────────────────────

const readerNav = document.querySelector('.reader-nav');
let lastNavY = 0;

window.addEventListener('scroll', () => {
  const y = window.scrollY;
  if (y > lastNavY && y > 56) {
    readerNav.classList.add('header-hidden');
  } else {
    readerNav.classList.remove('header-hidden');
  }
  lastNavY = y;
}, { passive: true });

// ── Start ─────────────────────────────────────────────────────────────────────

fetch('/api/auth/me').then(r => {
  if (!r.ok) { location.href = '/login'; return; }
  init().catch(err => {
    content.innerHTML = `<p style="padding:2rem;color:#c77">Error: ${err.message}</p>`;
  });
}).catch(() => { location.href = '/login'; });
