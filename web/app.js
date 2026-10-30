'use strict';

const $ = (id) => document.getElementById(id);
const THEMES = ['system', 'light', 'dark'];
const THEME_LABELS = { system: '跟随系统', light: '亮', dark: '暗' };

const state = {
  folders: [],
  byId: new Map(),
  pages: new Map(),
  expanded: new Set(),
  current: null,
  currentPages: [],
  searchTimer: 0,
  searchSeq: 0,
  treeLoaded: false,
};

/* ---------------- 基础工具 ---------------- */

async function api(path, options) {
  const res = await fetch(path, Object.assign({
    headers: { 'Accept': 'application/json' },
    credentials: 'same-origin',
  }, options || {}));
  let data = null;
  if (res.status !== 204) {
    try { data = await res.json(); } catch (e) { /* ignore */ }
  }
  if (!res.ok) {
    const msg = data && data.message ? data.message : ('HTTP ' + res.status);
    const err = new Error(msg);
    err.status = res.status;
    err.code = data && data.code;
    throw err;
  }
  return data;
}

function formatSize(n) {
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

function pageHash(folderID, filename) {
  return '#/f/' + folderID + '/' + encodeURIComponent(filename);
}

/* ---------------- 主题 ---------------- */

function themeMode() {
  try { return localStorage.getItem('theme') || 'system'; } catch (e) { return 'system'; }
}

function applyTheme(mode) {
  const dark = mode === 'dark' ||
    (mode === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.themeMode = mode;
  try { localStorage.setItem('theme', mode); } catch (e) { /* ignore */ }
  const btn = $('themeBtn');
  if (btn) {
    btn.title = '主题：' + THEME_LABELS[mode];
    btn.setAttribute('aria-label', '主题：' + THEME_LABELS[mode] + '（点击切换）');
  }
}

function cycleTheme() {
  const cur = themeMode();
  applyTheme(THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length]);
}

/* ---------------- 路由 ---------------- */

function parseHash() {
  let h = location.hash ? location.hash.slice(1) : '/';
  if (!h) h = '/';
  if (h === '/' || h === '/home') return { view: 'home' };
  if (h === '/about') return { view: 'about' };
  if (h === '/course') return { view: 'course' };
  const m = h.match(/^\/f\/(\d+)\/(.+)$/);
  if (m) {
    let filename = m[2];
    try { filename = decodeURIComponent(filename); } catch (e) { /* keep */ }
    return { view: 'page', folderID: Number(m[1]), filename: filename };
  }
  return { view: 'home' };
}

function setActiveNav(view) {
  const target = (view === 'page' || view === 'course') ? 'course' : view;
  document.querySelectorAll('.topbar nav a[data-nav]').forEach((a) => {
    a.classList.toggle('active', a.dataset.nav === target);
  });
}

async function route() {
  const r = parseHash();
  setActiveNav(r.view);
  closeSearch();
  closeDrawer();

  if (r.view === 'home' || r.view === 'about') {
    await showSite(r.view);
    return;
  }
  if (r.view === 'course') {
    await openCourseIndex();
    return;
  }
  await openPage(r.folderID, r.filename);
}

async function showSite(slug) {
  document.body.classList.remove('course-mode');
  $('homeView').hidden = false;
  $('courseView').hidden = true;
  $('siteTitle').textContent = '';
  $('siteBody').textContent = '加载中…';
  try {
    const data = await api('/api/site/' + encodeURIComponent(slug));
    $('siteTitle').textContent = data.title;
    $('siteBody').textContent = data.body;
    document.title = data.title;
  } catch (err) {
    $('siteTitle').textContent = '加载失败';
    $('siteBody').textContent = err.message;
    document.title = '加载失败';
  }
}

async function openCourseIndex() {
  await ensureTree();
  const last = readLastPage();
  if (last && last.folderID) {
    try {
      const pages = await ensurePages(last.folderID);
      if (pages.some((p) => p.filename === last.filename)) {
        location.replace(pageHash(last.folderID, last.filename));
        return;
      }
    } catch (e) { /* fall through */ }
  }
  const first = await firstVisiblePage();
  if (first) {
    location.replace(pageHash(first.folder_id, first.filename));
    return;
  }
  showCourseShell();
  state.current = null;
  state.currentPages = [];
  $('view').hidden = true;
  $('view').removeAttribute('src');
  $('stageEmpty').hidden = false;
  $('stageEmpty').textContent = '还没有可见的课件';
  $('courseTitle').textContent = '课件';
  $('prevBtn').disabled = true;
  $('nextBtn').disabled = true;
  renderTree();
}

function readLastPage() {
  try {
    const raw = localStorage.getItem('last_page') || '';
    const m = raw.match(/^#\/f\/(\d+)\/(.+)$/);
    if (!m) return null;
    return { folderID: Number(m[1]), filename: decodeURIComponent(m[2]) };
  } catch (e) {
    return null;
  }
}

async function firstVisiblePage() {
  const folders = state.folders.slice().sort((a, b) => a.sort - b.sort || a.id - b.id);
  for (const f of folders) {
    const pages = await ensurePages(f.id).catch(() => []);
    if (pages.length) return pages[0];
  }
  return null;
}

async function openPage(folderID, filename) {
  showCourseShell();
  await ensureTree();
  let pages = [];
  try {
    pages = await ensurePages(folderID);
  } catch (err) {
    showStageMessage(err.message);
    return;
  }
  const idx = pages.findIndex((p) => p.filename === filename);
  if (idx < 0) {
    state.current = null;
    state.currentPages = pages;
    showStageMessage('课件不存在或不可见');
    renderTree();
    return;
  }
  state.current = pages[idx];
  state.currentPages = pages;
  state.expanded.add(folderID);
  expandAncestors(folderID);
  renderTree();

  const page = pages[idx];
  const label = /^第\s*\d+\s*课/.test(page.title)
    ? page.title
    : '第 ' + (idx + 1) + ' 课 · ' + page.title;
  $('courseTitle').textContent = label;
  $('prevBtn').disabled = idx <= 0;
  $('nextBtn').disabled = idx >= pages.length - 1;
  $('stageEmpty').hidden = true;
  $('view').hidden = false;
  $('view').src = '/f/' + page.folder_id + '/' + encodeURIComponent(page.filename);
  document.title = page.title + ' · 课件';
  try { localStorage.setItem('last_page', pageHash(page.folder_id, page.filename)); } catch (e) { /* ignore */ }
}

function showCourseShell() {
  document.body.classList.add('course-mode');
  $('homeView').hidden = true;
  $('courseView').hidden = false;
}

function showStageMessage(msg) {
  $('view').hidden = true;
  $('view').removeAttribute('src');
  $('stageEmpty').hidden = false;
  $('stageEmpty').textContent = msg;
  $('courseTitle').textContent = '课件';
  $('prevBtn').disabled = true;
  $('nextBtn').disabled = true;
  document.title = '课件';
}

/* ---------------- 课程树 ---------------- */

async function ensureTree(force) {
  if (state.treeLoaded && !force) return;
  const data = await api('/api/tree');
  state.folders = data.folders || [];
  state.byId = new Map(state.folders.map((f) => [f.id, f]));
  state.treeLoaded = true;
}

async function ensurePages(folderID) {
  if (state.pages.has(folderID)) return state.pages.get(folderID);
  const data = await api('/api/pages?folder_id=' + encodeURIComponent(folderID));
  const pages = data.items || [];
  state.pages.set(folderID, pages);
  return pages;
}

function childrenOf(parentID) {
  return state.folders
    .filter((f) => {
      if (parentID === 1) {
        return f.parent_id === 1 || !state.byId.has(f.parent_id);
      }
      return f.parent_id === parentID;
    })
    .sort((a, b) => a.sort - b.sort || a.id - b.id);
}

function expandAncestors(folderID) {
  let cur = state.byId.get(folderID);
  let guard = 0;
  while (cur && cur.parent_id !== 1 && guard++ < 64) {
    state.expanded.add(cur.parent_id);
    cur = state.byId.get(cur.parent_id);
  }
}

function renderTree() {
  const root = $('tree');
  root.textContent = '';
  if (!state.folders.length) {
    const empty = document.createElement('div');
    empty.className = 'tree-empty';
    empty.textContent = '还没有公开课程';
    root.appendChild(empty);
    return;
  }
  const list = document.createElement('ul');
  list.className = 'tree';
  renderFolderChildren(1, list);
  root.appendChild(list);
}

function renderFolderChildren(parentID, ul) {
  for (const folder of childrenOf(parentID)) {
    const li = document.createElement('li');
    const row = document.createElement('div');
    row.className = 'tree-row';

    const twist = document.createElement('button');
    twist.type = 'button';
    twist.className = 'twist';
    twist.setAttribute('aria-label', state.expanded.has(folder.id) ? '收起' : '展开');
    twist.textContent = state.expanded.has(folder.id) ? '▾' : '▸';
    twist.addEventListener('click', (ev) => {
      ev.stopPropagation();
      toggleFolder(folder.id);
    });
    row.appendChild(twist);

    const name = document.createElement('button');
    name.type = 'button';
    name.className = 'folder' + (state.current && state.current.folder_id === folder.id ? ' active' : '');
    name.textContent = folder.name;
    name.addEventListener('click', () => toggleFolder(folder.id, true));
    row.appendChild(name);

    if (folder.page_count > 0) {
      const count = document.createElement('span');
      count.className = 'count';
      count.textContent = String(folder.page_count);
      row.appendChild(count);
    }
    li.appendChild(row);

    if (state.expanded.has(folder.id)) {
      const sub = document.createElement('ul');
      renderFolderChildren(folder.id, sub);
      const pages = state.pages.get(folder.id);
      if (pages === undefined) {
        const loading = document.createElement('li');
        loading.className = 'tree-empty';
        loading.textContent = '加载中…';
        sub.appendChild(loading);
        ensurePages(folder.id).then(renderTree).catch(() => {});
      } else {
        for (const page of pages) {
          const pli = document.createElement('li');
          const prow = document.createElement('div');
          prow.className = 'tree-row';
          const spacer = document.createElement('span');
          spacer.className = 'twist';
          prow.appendChild(spacer);
          const link = document.createElement('button');
          link.type = 'button';
          link.className = 'page-link' +
            (state.current && state.current.folder_id === page.folder_id &&
             state.current.filename === page.filename ? ' active' : '');
          link.textContent = page.title;
          link.title = page.filename;
          link.addEventListener('click', () => {
            location.hash = pageHash(page.folder_id, page.filename);
          });
          prow.appendChild(link);
          pli.appendChild(prow);
          sub.appendChild(pli);
        }
      }
      li.appendChild(sub);
    }
    ul.appendChild(li);
  }
}

function toggleFolder(folderID, forceOpen) {
  if (state.expanded.has(folderID) && !forceOpen) {
    state.expanded.delete(folderID);
  } else {
    state.expanded.add(folderID);
    ensurePages(folderID).then(renderTree).catch((err) => {
      console.error(err);
    });
  }
  renderTree();
}

/* ---------------- 搜索 ---------------- */

function closeSearch() {
  $('searchResults').hidden = true;
}

async function runSearch(q) {
  const seq = ++state.searchSeq;
  try {
    const data = await api('/api/search?q=' + encodeURIComponent(q));
    if (seq !== state.searchSeq) return;
    renderSearchResults(data);
  } catch (err) {
    if (seq !== state.searchSeq) return;
    const box = $('searchResults');
    box.textContent = '';
    const div = document.createElement('div');
    div.className = 'search-empty';
    div.textContent = err.message;
    box.appendChild(div);
    box.hidden = false;
  }
}

function renderSearchResults(data) {
  const box = $('searchResults');
  box.textContent = '';
  if (!data.items.length) {
    const div = document.createElement('div');
    div.className = 'search-empty';
    div.textContent = '没有找到「' + data.query + '」';
    box.appendChild(div);
    box.hidden = false;
    return;
  }
  for (const item of data.items) {
    const a = document.createElement('a');
    a.className = 'search-result';
    a.href = pageHash(item.folder_id, item.filename);
    const strong = document.createElement('strong');
    strong.textContent = item.title;
    const span = document.createElement('span');
    span.textContent = item.snippet || item.filename;
    a.appendChild(strong);
    a.appendChild(span);
    a.addEventListener('click', () => {
      $('searchInput').value = '';
      closeSearch();
    });
    box.appendChild(a);
  }
  box.hidden = false;
}

/* ---------------- 抽屉 / 全屏 ---------------- */

function openDrawer() {
  document.body.classList.add('drawer-open');
}
function closeDrawer() {
  document.body.classList.remove('drawer-open');
}

/* ---------------- 初始化 ---------------- */

function init() {
  applyTheme(themeMode());
  $('themeBtn').addEventListener('click', cycleTheme);
  $('drawerBtn').addEventListener('click', openDrawer);
  $('backdrop').addEventListener('click', closeDrawer);
  $('backBtn').addEventListener('click', () => { location.hash = '#/'; });
  $('fsBtn').addEventListener('click', () => {
    const stage = $('stage');
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
    } else if (stage.requestFullscreen) {
      stage.requestFullscreen().catch(() => {});
    }
  });
  $('prevBtn').addEventListener('click', () => {
    const idx = state.currentPages.findIndex((p) => state.current && p.filename === state.current.filename && p.folder_id === state.current.folder_id);
    if (idx > 0) {
      const p = state.currentPages[idx - 1];
      location.hash = pageHash(p.folder_id, p.filename);
    }
  });
  $('nextBtn').addEventListener('click', () => {
    const idx = state.currentPages.findIndex((p) => state.current && p.filename === state.current.filename && p.folder_id === state.current.folder_id);
    if (idx >= 0 && idx < state.currentPages.length - 1) {
      const p = state.currentPages[idx + 1];
      location.hash = pageHash(p.folder_id, p.filename);
    }
  });

  const search = $('searchInput');
  search.addEventListener('input', () => {
    clearTimeout(state.searchTimer);
    const q = search.value.trim();
    if (!q) {
      closeSearch();
      return;
    }
    state.searchTimer = setTimeout(() => runSearch(q), 250);
  });
  search.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') {
      search.value = '';
      closeSearch();
      search.blur();
    }
  });
  document.addEventListener('click', (ev) => {
    const wrap = document.querySelector('.search-wrap');
    if (wrap && !wrap.contains(ev.target)) closeSearch();
  });

  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const onMedia = () => { if (themeMode() === 'system') applyTheme('system'); };
  if (media.addEventListener) media.addEventListener('change', onMedia);
  else if (media.addListener) media.addListener(onMedia);

  window.addEventListener('hashchange', () => { route().catch((err) => console.error(err)); });
  route().catch((err) => {
    console.error(err);
    showStageMessage(err.message);
  });
}

document.addEventListener('DOMContentLoaded', init);
