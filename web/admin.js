'use strict';

const $ = (id) => document.getElementById(id);
const THEMES = ['system', 'light', 'dark'];
const THEME_LABELS = { system: '跟随系统', light: '亮', dark: '暗' };

const state = {
  folders: [],
  byId: new Map(),
  selected: 1,
  pages: [],
  expanded: new Set(),
  treeInit: false,
  dragFolder: null,
  dragPage: null,
  creatingParent: null,
  renamingFolder: null,
  editingPage: null,
};

/* ---------------- 工具 ---------------- */

async function api(path, options) {
  const opts = Object.assign({
    credentials: 'same-origin',
    headers: { 'Accept': 'application/json' },
  }, options || {});
  if (typeof opts.body === 'string' && !opts.headers['Content-Type']) {
    opts.headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, opts);
  let data = null;
  if (res.status !== 204) {
    try { data = await res.json(); } catch (e) { /* ignore */ }
  }
  if (res.status === 401) {
    location.href = '/admin/login';
    throw new Error('请先登录');
  }
  if (!res.ok) {
    const err = new Error(data && data.message ? data.message : ('HTTP ' + res.status));
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

function formatTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' +
    p(d.getHours()) + ':' + p(d.getMinutes());
}

function showNotice(el, msg, kind) {
  el.className = 'notice' + (kind ? ' ' + kind : '');
  el.textContent = msg;
  el.hidden = false;
}

function clearNotice(el) {
  el.hidden = true;
  el.textContent = '';
}

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
  if (btn) btn.title = '主题：' + THEME_LABELS[mode];
}

/* ---------------- 课程树 ---------------- */

function childrenOf(parentID) {
  return state.folders
    .filter((f) => f.parent_id === parentID)
    .sort((a, b) => a.sort - b.sort || a.id - b.id);
}

async function loadTree() {
  const data = await api('/admin/api/tree');
  state.folders = data.folders || [];
  state.byId = new Map(state.folders.map((f) => [f.id, f]));
  if (!state.treeInit) {
    for (const f of state.folders) state.expanded.add(f.id);
    state.treeInit = true;
  }
  if (state.selected !== 1 && !state.byId.has(state.selected)) state.selected = 1;
  renderAdminTree();
}

function renderAdminTree() {
  const root = $('adminTree');
  root.textContent = '';

  const list = document.createElement('ul');
  list.className = 'tree';
  list.appendChild(rootRow());
  renderFolderChildren(1, list);
  root.appendChild(list);
}

function rootRow() {
  const li = document.createElement('li');
  const row = document.createElement('div');
  row.className = 'tree-row' + (state.selected === 1 ? ' selected' : '');

  const twist = document.createElement('span');
  twist.className = 'twist';
  twist.textContent = state.expanded.has(1) ? '▾' : '▸';
  row.appendChild(twist);

  const name = document.createElement('button');
  name.type = 'button';
  name.className = 'folder';
  name.textContent = '根目录';
  name.addEventListener('click', () => {
    state.expanded.add(1);
    selectFolder(1);
  });
  row.appendChild(name);

  const badge = document.createElement('span');
  badge.className = 'badge priv';
  badge.textContent = '私有';
  row.appendChild(badge);

  const menu = document.createElement('button');
  menu.type = 'button';
  menu.className = 'icon-btn';
  menu.style.width = '26px';
  menu.style.height = '26px';
  menu.textContent = '⋯';
  menu.title = '操作';
  menu.addEventListener('click', (ev) => {
    ev.stopPropagation();
    showFolderMenu(ev, { id: 1, parent_id: 1, name: '_root', is_public: 0 });
  });
  row.appendChild(menu);

  row.addEventListener('contextmenu', (ev) => {
    ev.preventDefault();
    showFolderMenu(ev, { id: 1, parent_id: 1, name: '_root', is_public: 0 });
  });
  attachDrop(row, 1);

  li.appendChild(row);
  return li;
}

function renderFolderChildren(parentID, ul) {
  if (state.creatingParent === parentID) ul.appendChild(newFolderRow(parentID));

  for (const f of childrenOf(parentID)) {
    const li = document.createElement('li');
    const row = document.createElement('div');
    row.className = 'tree-row' + (state.selected === f.id ? ' selected' : '');
    row.draggable = true;

    const twist = document.createElement('button');
    twist.type = 'button';
    twist.className = 'twist';
    twist.textContent = state.expanded.has(f.id) ? '▾' : '▸';
    twist.addEventListener('click', (ev) => {
      ev.stopPropagation();
      if (state.expanded.has(f.id)) state.expanded.delete(f.id);
      else state.expanded.add(f.id);
      renderAdminTree();
    });
    row.appendChild(twist);

    if (state.renamingFolder === f.id) {
      row.appendChild(folderNameInput(f));
    } else {
      const name = document.createElement('button');
      name.type = 'button';
      name.className = 'folder';
      name.textContent = f.name;
      name.title = '双击改名';
      name.addEventListener('click', () => {
        state.expanded.add(f.id);
        selectFolder(f.id);
      });
      name.addEventListener('dblclick', (ev) => {
        ev.preventDefault();
        startRenameFolder(f);
      });
      row.appendChild(name);
    }

    const badge = document.createElement('span');
    badge.className = 'badge ' + (f.is_public === 1 ? 'pub' : (f.is_public === 0 ? 'priv' : ''));
    badge.textContent = f.is_public === 1 ? '公开' : (f.is_public === 0 ? '私有' : '继承');
    row.appendChild(badge);

    if (f.page_count > 0) {
      const count = document.createElement('span');
      count.className = 'count';
      count.textContent = String(f.page_count);
      row.appendChild(count);
    }

    const menu = document.createElement('button');
    menu.type = 'button';
    menu.className = 'icon-btn';
    menu.style.width = '26px';
    menu.style.height = '26px';
    menu.textContent = '⋯';
    menu.title = '操作';
    menu.addEventListener('click', (ev) => {
      ev.stopPropagation();
      showFolderMenu(ev, f);
    });
    row.appendChild(menu);

    row.addEventListener('contextmenu', (ev) => {
      ev.preventDefault();
      showFolderMenu(ev, f);
    });
    row.addEventListener('dragstart', (ev) => {
      state.dragFolder = { id: f.id, parentID: f.parent_id };
      ev.dataTransfer.effectAllowed = 'move';
      ev.dataTransfer.setData('text/plain', String(f.id));
    });
    row.addEventListener('dragend', clearDrag);
    attachDrop(row, f.id);

    li.appendChild(row);

    if (state.expanded.has(f.id)) {
      const sub = document.createElement('ul');
      renderFolderChildren(f.id, sub);
      li.appendChild(sub);
    }
    ul.appendChild(li);
  }
}

function clearDrag() {
  state.dragFolder = null;
  state.dragPage = null;
  document.querySelectorAll('.drag-over').forEach((el) => el.classList.remove('drag-over'));
}

// 判断 id 是否是 ancestorID 的后代，用于阻止文件夹拖进自己的子树。
function isDescendant(id, ancestorID) {
  let cur = id;
  for (let i = 0; i < 64 && cur !== 1; i++) {
    const f = state.byId.get(cur);
    if (!f) return false;
    if (f.parent_id === ancestorID) return true;
    cur = f.parent_id;
  }
  return false;
}

// 文件夹行同时接收文件夹和课件：拖进来就是移动到该文件夹。
function attachDrop(row, targetID) {
  row.addEventListener('dragover', (ev) => {
    if (!state.dragFolder && !state.dragPage) return;
    if (state.dragFolder) {
      if (state.dragFolder.id === targetID || isDescendant(targetID, state.dragFolder.id)) return;
    }
    if (state.dragPage && state.dragPage.folderID === targetID) return;
    ev.preventDefault();
    row.classList.add('drag-over');
  });
  row.addEventListener('dragleave', () => row.classList.remove('drag-over'));
  row.addEventListener('drop', (ev) => {
    ev.preventDefault();
    row.classList.remove('drag-over');
    const folder = state.dragFolder;
    const page = state.dragPage;
    if (folder) {
      moveFolderInto(folder.id, targetID).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    } else if (page) {
      movePageTo(page.id, targetID).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    }
  });
}

function newFolderRow(parentID) {
  const li = document.createElement('li');
  const row = document.createElement('div');
  row.className = 'tree-row editing';
  row.appendChild(inlineInput('', '新文件夹名称，回车创建', (submit, name) => {
    state.creatingParent = null;
    if (submit && name) {
      createFolder(parentID, name).catch((err) => {
        showNotice($('pagesNotice'), err.message, 'err');
        renderAdminTree();
      });
    } else {
      renderAdminTree();
    }
  }, 64));
  li.appendChild(row);
  return li;
}

function folderNameInput(f) {
  return inlineInput(f.name, '文件夹名称', (submit, name) => {
    state.renamingFolder = null;
    renderAdminTree();
    if (submit && name && name !== f.name) {
      renameFolder(f, name).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    }
  }, 64);
}

// 共用的内联输入：回车提交，Esc 或失焦取消。
function inlineInput(value, placeholder, done, maxLength) {
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'inline-input';
  input.value = value;
  input.placeholder = placeholder;
  input.maxLength = maxLength;
  let finished = false;
  const finish = (submit) => {
    if (finished) return;
    finished = true;
    done(submit, input.value.trim());
  };
  input.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter') {
      ev.preventDefault();
      ev.stopPropagation();
      finish(true);
    } else if (ev.key === 'Escape') {
      ev.preventDefault();
      ev.stopPropagation();
      finish(false);
    } else {
      ev.stopPropagation();
    }
  });
  input.addEventListener('blur', () => finish(false));
  requestAnimationFrame(() => {
    input.focus();
    input.select();
  });
  return input;
}

function startCreateFolder(parentID) {
  hideMenu();
  state.renamingFolder = null;
  state.creatingParent = parentID;
  state.expanded.add(parentID);
  renderAdminTree();
}

function startRenameFolder(f) {
  hideMenu();
  state.creatingParent = null;
  state.renamingFolder = f.id;
  state.expanded.add(f.parent_id);
  renderAdminTree();
}

async function createFolder(parentID, name) {
  const f = await api('/admin/api/folders', {
    method: 'POST',
    body: JSON.stringify({ parent_id: parentID, name: name }),
  });
  state.expanded.add(parentID);
  state.expanded.add(f.id);
  await loadTree();
  selectFolder(f.id);
  showNotice($('pagesNotice'), '已创建文件夹：' + f.name, 'ok');
}

async function renameFolder(folder, name) {
  await api('/admin/api/folders/' + folder.id, {
    method: 'PATCH',
    body: JSON.stringify({ name: name }),
  });
  await loadTree();
  showNotice($('pagesNotice'), '已改名', 'ok');
}

async function moveFolderInto(id, targetID) {
  const f = state.byId.get(id);
  if (!f || f.parent_id === targetID) return;
  if (id === targetID || isDescendant(targetID, id)) {
    throw new Error('不能移动到自身或自己的子文件夹');
  }
  const siblings = childrenOf(targetID).filter((x) => x.id !== id);
  const sort = siblings.length ? siblings[siblings.length - 1].sort + 1000 : 1000;
  await api('/admin/api/folders/' + id, {
    method: 'PATCH',
    body: JSON.stringify({ parent_id: targetID, sort: sort }),
  });
  state.expanded.add(targetID);
  await loadTree();
  showNotice($('pagesNotice'), '已移动文件夹', 'ok');
}

async function movePageTo(id, targetID) {
  const p = state.pages.find((x) => x.id === id);
  if (!p || p.folder_id === targetID) return;
  await api('/admin/api/pages/' + encodeURIComponent(id), {
    method: 'PATCH',
    body: JSON.stringify({ folder_id: targetID }),
  });
  state.expanded.add(targetID);
  await loadTree();
  await loadPages(state.selected);
  showNotice($('pagesNotice'), '课件已移动', 'ok');
}

// 文件夹排序：和相邻兄弟交换，然后按 1000 重新编号。
async function moveSibling(folder, delta) {
  const siblings = childrenOf(folder.parent_id);
  const i = siblings.findIndex((x) => x.id === folder.id);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= siblings.length) return;
  const order = siblings.slice();
  [order[i], order[j]] = [order[j], order[i]];
  for (let k = 0; k < order.length; k++) {
    const sort = (k + 1) * 1000;
    if (order[k].sort !== sort) {
      await api('/admin/api/folders/' + order[k].id, {
        method: 'PATCH',
        body: JSON.stringify({ sort: sort }),
      });
    }
  }
  await loadTree();
  showNotice($('pagesNotice'), '顺序已更新', 'ok');
}

function selectFolder(id) {
  state.selected = id;
  renderAdminTree();
  const folder = id === 1 ? { name: '根目录' } : state.byId.get(id);
  $('pagesTitle').textContent = folder ? folder.name + ' · 课件' : '课件';
  loadPages(id).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
}

/* ---------------- 文件夹操作 ---------------- */

function showFolderMenu(ev, folder) {
  const menu = $('ctxMenu');
  menu.textContent = '';

  const add = (label, fn, cls) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    if (cls) b.className = cls;
    b.addEventListener('click', () => {
      hideMenu();
      Promise.resolve().then(fn).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    });
    menu.appendChild(b);
  };

  add('新建子文件夹', () => startCreateFolder(folder.id));
  if (folder.id !== 1) {
    add('改名', () => startRenameFolder(folder));
    add('上移', () => moveSibling(folder, -1));
    add('下移', () => moveSibling(folder, 1));
    const sep = document.createElement('div');
    sep.className = 'sep';
    menu.appendChild(sep);
    add('可见性：继承', () => setVisibility(folder.id, 'inherit'));
    add('可见性：公开', () => setVisibility(folder.id, 1));
    add('可见性：私有', () => setVisibility(folder.id, 0));
    const sep2 = document.createElement('div');
    sep2.className = 'sep';
    menu.appendChild(sep2);

    // 两段式删除：第一次点变“确认”，4 秒内再点才真正删除。
    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'danger';
    del.textContent = '删除';
    let armed = false;
    del.addEventListener('click', () => {
      if (!armed) {
        armed = true;
        del.textContent = '确认删除（不可恢复）';
        del.classList.add('armed');
        setTimeout(() => {
          armed = false;
          del.textContent = '删除';
          del.classList.remove('armed');
        }, 4000);
        return;
      }
      hideMenu();
      deleteFolder(folder).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    });
    menu.appendChild(del);
  }

  menu.hidden = false;
  const rect = menu.getBoundingClientRect();
  let x = ev.clientX || 20;
  let y = ev.clientY || 20;
  if (x + rect.width > window.innerWidth - 8) x = window.innerWidth - rect.width - 8;
  if (y + rect.height > window.innerHeight - 8) y = window.innerHeight - rect.height - 8;
  menu.style.left = Math.max(8, x) + 'px';
  menu.style.top = Math.max(8, y) + 'px';
}

function hideMenu() {
  $('ctxMenu').hidden = true;
}

async function setVisibility(id, value) {
  await api('/admin/api/folders/' + id, {
    method: 'PATCH',
    body: JSON.stringify({ is_public: value }),
  });
  await loadTree();
  showNotice($('pagesNotice'), '可见性已更新', 'ok');
}

async function deleteFolder(folder) {
  const data = await api('/admin/api/folders/' + folder.id + '?recursive=1', { method: 'DELETE' });
  if (state.selected === folder.id) state.selected = folder.parent_id;
  await loadTree();
  selectFolder(state.selected);
  showNotice($('pagesNotice'),
    '已删除 ' + data.deleted_folders + ' 个文件夹、' + data.deleted_pages + ' 个课件', 'ok');
}

/* ---------------- 课件列表 ---------------- */

async function loadPages(folderID) {
  clearNotice($('pagesNotice'));
  const data = await api('/admin/api/pages?folder_id=' + encodeURIComponent(folderID));
  state.pages = data.items || [];
  renderPages();
}

function renderPages() {
  const list = $('pagesList');
  list.textContent = '';
  if (!state.pages.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.style.margin = '12px';
    empty.textContent = '这个文件夹还没有课件';
    list.appendChild(empty);
    return;
  }
  for (const p of state.pages) {
    const row = document.createElement('div');
    row.className = 'list-row';
    row.draggable = true;

    const main = document.createElement('div');
    main.className = 'list-main';
    if (state.editingPage === p.id) {
      main.appendChild(pageTitleInput(p));
    } else {
      const title = document.createElement('div');
      title.className = 'list-title';
      title.textContent = p.title;
      title.title = '双击改标题';
      title.addEventListener('dblclick', () => startEditPageTitle(p));
      main.appendChild(title);
    }
    const sub = document.createElement('div');
    sub.className = 'list-sub';
    sub.textContent = p.filename + ' · ' + formatSize(p.size) + ' · ' + formatTime(p.updated_at);
    main.appendChild(sub);
    row.appendChild(main);

    const actions = document.createElement('div');
    actions.className = 'list-actions';
    actions.appendChild(actionBtn('标题', () => startEditPageTitle(p)));
    const preview = document.createElement('a');
    preview.className = 'btn small';
    preview.href = '/admin/preview/' + encodeURIComponent(p.id);
    preview.target = '_blank';
    preview.rel = 'noopener';
    preview.textContent = '预览';
    actions.appendChild(preview);
    actions.appendChild(actionBtn('删除',
      (btn) => armDanger(btn, '确认删除', () => deletePage(p), $('pagesNotice')), 'danger'));
    row.appendChild(actions);

    row.addEventListener('dragstart', (ev) => {
      state.dragPage = { id: p.id, folderID: p.folder_id };
      ev.dataTransfer.effectAllowed = 'move';
      ev.dataTransfer.setData('text/plain', p.id);
    });
    row.addEventListener('dragend', clearDrag);
    row.addEventListener('dragover', (ev) => {
      if (!state.dragPage || state.dragPage.folderID !== p.folder_id || state.dragPage.id === p.id) return;
      ev.preventDefault();
      row.classList.add('drag-over');
    });
    row.addEventListener('dragleave', () => row.classList.remove('drag-over'));
    row.addEventListener('drop', (ev) => {
      ev.preventDefault();
      row.classList.remove('drag-over');
      if (!state.dragPage) return;
      reorder('page', state.dragPage.id, p.id)
        .then(() => loadPages(state.selected))
        .catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    });

    list.appendChild(row);
  }
}

function actionBtn(label, fn, cls) {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = 'btn small' + (cls ? ' ' + cls : '');
  b.textContent = label;
  b.addEventListener('click', () => {
    Promise.resolve().then(() => fn(b)).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
  });
  return b;
}

// 两段式危险按钮：第一次点只改名，4 秒内再点才执行。
function armDanger(btn, label, fn, noticeEl) {
  if (btn.dataset.armed === '1') {
    delete btn.dataset.armed;
    btn.disabled = true;
    const reset = () => {
      btn.disabled = false;
      btn.textContent = btn.dataset.original;
      btn.classList.remove('armed');
    };
    Promise.resolve().then(fn).then(reset).catch((err) => {
      reset();
      showNotice(noticeEl, err.message, 'err');
    });
    return;
  }
  btn.dataset.armed = '1';
  btn.dataset.original = btn.textContent;
  btn.textContent = label;
  btn.classList.add('armed');
  setTimeout(() => {
    if (btn.dataset.armed === '1') {
      delete btn.dataset.armed;
      btn.textContent = btn.dataset.original;
      btn.classList.remove('armed');
    }
  }, 4000);
}

function startEditPageTitle(p) {
  state.editingPage = p.id;
  renderPages();
}

function pageTitleInput(p) {
  return inlineInput(p.title, '课件标题', (submit, title) => {
    state.editingPage = null;
    renderPages();
    if (submit && title && title !== p.title) {
      renamePage(p, title).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
    }
  }, 200);
}

async function renamePage(p, title) {
  await api('/admin/api/pages/' + encodeURIComponent(p.id), {
    method: 'PATCH',
    body: JSON.stringify({ title: title }),
  });
  await loadPages(state.selected);
  showNotice($('pagesNotice'), '标题已更新', 'ok');
}

async function deletePage(p) {
  await api('/admin/api/pages/' + encodeURIComponent(p.id), { method: 'DELETE' });
  await loadTree();
  await loadPages(state.selected);
  showNotice($('pagesNotice'), '已删除课件', 'ok');
}

/* ---------------- 上传 / 导入 ---------------- */

async function uploadFile(file) {
  const fd = new FormData();
  fd.append('folder_id', String(state.selected));
  fd.append('file', file, file.name);
  const res = await fetch('/admin/api/pages', { method: 'POST', body: fd, credentials: 'same-origin' });
  let data = null;
  try { data = await res.json(); } catch (e) { /* ignore */ }
  if (res.status === 401) { location.href = '/admin/login'; throw new Error('请先登录'); }
  if (!res.ok) throw new Error(data && data.message ? data.message : ('HTTP ' + res.status));
  const labels = { created: '新建', updated: '覆盖', skipped: '跳过（内容相同）' };
  showNotice($('pagesNotice'), '上传结果：' + (labels[data.status] || data.status) + ' · ' + data.title, 'ok');
  await loadTree();
  await loadPages(state.selected);
}

async function importFiles(files) {
  if (!files.length) return;
  const fd = new FormData();
  fd.append('target_folder_id', String(state.selected));
  for (const f of files) {
    fd.append('files', f, f.webkitRelativePath || f.name);
  }
  const res = await fetch('/admin/api/import', { method: 'POST', body: fd, credentials: 'same-origin' });
  let data = null;
  try { data = await res.json(); } catch (e) { /* ignore */ }
  if (res.status === 401) { location.href = '/admin/login'; throw new Error('请先登录'); }
  if (!res.ok) throw new Error(data && data.message ? data.message : ('HTTP ' + res.status));
  let msg = '导入完成：新建 ' + data.created + '，覆盖 ' + data.updated + '，跳过 ' + data.skipped;
  if (data.folders_created && data.folders_created.length) {
    msg += '；新建文件夹：' + data.folders_created.join('、');
  }
  if (data.ignored && data.ignored.length) {
    msg += '；忽略 ' + data.ignored.length + ' 个非 HTML 文件';
  }
  if (data.errors && data.errors.length) {
    msg += '；错误 ' + data.errors.length + ' 个：' +
      data.errors.map((e) => e.path + ' (' + e.reason + ')').join('，');
  }
  showNotice($('pagesNotice'), msg, data.errors && data.errors.length ? '' : 'ok');
  await loadTree();
  await loadPages(state.selected);
}

/* ---------------- 排序 ---------------- */

async function reorder(kind, draggedID, targetID) {
  let siblings;
  if (kind === 'folder') {
    const dragged = state.byId.get(draggedID);
    if (!dragged) return;
    siblings = childrenOf(dragged.parent_id);
  } else {
    siblings = state.pages.slice().sort((a, b) => a.sort - b.sort || a.created_at - b.created_at);
  }
  const from = siblings.findIndex((x) => x.id === draggedID);
  const to = siblings.findIndex((x) => x.id === targetID);
  if (from < 0 || to < 0 || from === to) return;

  const item = siblings.splice(from, 1)[0];
  siblings.splice(to, 0, item);
  const idx = siblings.indexOf(item);
  const prev = siblings[idx - 1];
  const next = siblings[idx + 1];

  let sort;
  if (prev && next) {
    if (next.sort - prev.sort < 2) {
      for (let i = 0; i < siblings.length; i++) {
        const s = (i + 1) * 1000;
        if (siblings[i].sort !== s) await patchSort(kind, siblings[i].id, s);
      }
      await loadTree();
      return;
    }
    sort = Math.floor((prev.sort + next.sort) / 2);
  } else if (prev) {
    sort = prev.sort + 1000;
  } else if (next) {
    sort = next.sort - 1000;
  } else {
    sort = 1000;
  }
  await patchSort(kind, draggedID, sort);
  await loadTree();
}

async function patchSort(kind, id, sort) {
  const path = kind === 'folder'
    ? '/admin/api/folders/' + id
    : '/admin/api/pages/' + encodeURIComponent(id);
  await api(path, { method: 'PATCH', body: JSON.stringify({ sort: sort }) });
}

/* ---------------- 站点页面 / 备份 / 会话 ---------------- */

async function loadSite(slug) {
  clearNotice($('siteNotice'));
  try {
    const data = await api('/admin/api/site/' + encodeURIComponent(slug));
    $('siteTitle').value = data.title;
    $('siteBody').value = data.body;
  } catch (err) {
    showNotice($('siteNotice'), err.message, 'err');
  }
}

async function saveSite() {
  const slug = $('siteSlug').value;
  const data = await api('/admin/api/site/' + encodeURIComponent(slug), {
    method: 'POST',
    body: JSON.stringify({ title: $('siteTitle').value, body: $('siteBody').value }),
  });
  showNotice($('siteNotice'), '已保存（' + formatTime(data.updated_at) + '）', 'ok');
}

async function backup() {
  clearNotice($('backupNotice'));
  try {
    const data = await api('/admin/api/backup', { method: 'POST' });
    showNotice($('backupNotice'), '备份完成：' + data.file + ' · ' + formatSize(data.size), 'ok');
  } catch (err) {
    showNotice($('backupNotice'), err.message, 'err');
  }
}

async function loadSessions() {
  clearNotice($('sessionsNotice'));
  const list = $('sessionsList');
  list.textContent = '';
  try {
    const data = await api('/admin/api/sessions');
    if (!data.items.length) {
      const empty = document.createElement('div');
      empty.className = 'empty';
      empty.style.margin = '12px';
      empty.textContent = '没有活跃会话';
      list.appendChild(empty);
      return;
    }
    const table = document.createElement('table');
    table.className = 'table';
    const thead = document.createElement('thead');
    thead.innerHTML = '<tr><th>会话</th><th>创建</th><th>最后活跃</th><th>IP</th><th>UA</th><th></th></tr>';
    table.appendChild(thead);
    const tbody = document.createElement('tbody');
    for (const s of data.items) {
      const tr = document.createElement('tr');
      const idTd = document.createElement('td');
      idTd.className = 'mono nowrap';
      idTd.textContent = s.id;
      if (s.current) {
        const badge = document.createElement('span');
        badge.className = 'badge pub';
        badge.style.marginLeft = '6px';
        badge.textContent = '当前';
        idTd.appendChild(badge);
      }
      tr.appendChild(idTd);
      tr.appendChild(td(formatTime(s.created_at)));
      tr.appendChild(td(formatTime(s.renewed_at)));
      tr.appendChild(td(s.ip || '—'));
      const ua = td(s.ua || '—');
      ua.style.maxWidth = '360px';
      ua.style.overflow = 'hidden';
      ua.style.textOverflow = 'ellipsis';
      tr.appendChild(ua);
      const actionTd = document.createElement('td');
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'btn small danger';
      b.textContent = '踢掉';
      b.addEventListener('click', () => {
        armDanger(b, '确认踢掉', () => deleteSession(s.id), $('sessionsNotice'));
      });
      actionTd.appendChild(b);
      tr.appendChild(actionTd);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    list.appendChild(table);
  } catch (err) {
    showNotice($('sessionsNotice'), err.message, 'err');
  }
}

function td(text) {
  const el = document.createElement('td');
  el.textContent = text;
  return el;
}

async function deleteSession(id) {
  await api('/admin/api/sessions/' + encodeURIComponent(id), { method: 'DELETE' });
  await loadSessions();
  showNotice($('sessionsNotice'), '已踢掉会话', 'ok');
}

async function deleteOtherSessions() {
  const data = await api('/admin/api/sessions', { method: 'DELETE' });
  await loadSessions();
  showNotice($('sessionsNotice'), '已删除 ' + data.deleted + ' 个会话', 'ok');
}

/* ---------------- 标签页 / 初始化 ---------------- */

function showTab(name) {
  document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.dataset.tab === name));
  for (const id of ['pages', 'site', 'backup', 'sessions']) {
    $('tab-' + id).hidden = id !== name;
  }
  if (name === 'site') loadSite($('siteSlug').value);
  if (name === 'sessions') loadSessions();
}

async function logout() {
  try {
    await api('/admin/logout', { method: 'POST' });
  } catch (e) { /* ignore */ }
  location.href = '/admin/login';
}

function init() {
  applyTheme(themeMode());
  $('themeBtn').addEventListener('click', () => {
    const cur = themeMode();
    applyTheme(THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length]);
  });
  $('logoutBtn').addEventListener('click', logout);

  document.querySelectorAll('.tab').forEach((t) => {
    t.addEventListener('click', () => showTab(t.dataset.tab));
  });

  $('newFolderBtn').addEventListener('click', () => startCreateFolder(state.selected));
  $('refreshTreeBtn').addEventListener('click', () => {
    loadTree().catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
  });
  $('uploadBtn').addEventListener('click', () => $('uploadInput').click());
  $('uploadInput').addEventListener('change', () => {
    const file = $('uploadInput').files[0];
    $('uploadInput').value = '';
    if (file) uploadFile(file).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
  });
  $('importBtn').addEventListener('click', () => $('importInput').click());
  $('importInput').addEventListener('change', () => {
    const files = Array.from($('importInput').files);
    $('importInput').value = '';
    if (files.length) importFiles(files).catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
  });

  $('siteSlug').addEventListener('change', () => loadSite($('siteSlug').value));
  $('saveSiteBtn').addEventListener('click', () => {
    saveSite().catch((err) => showNotice($('siteNotice'), err.message, 'err'));
  });
  $('backupBtn').addEventListener('click', backup);
  $('refreshSessionsBtn').addEventListener('click', loadSessions);
  $('deleteOtherSessionsBtn').addEventListener('click', (ev) => {
    armDanger(ev.currentTarget, '确认全部踢掉', () => deleteOtherSessions(), $('sessionsNotice'));
  });

  document.addEventListener('click', (ev) => {
    if (!$('ctxMenu').contains(ev.target)) hideMenu();
  });
  document.addEventListener('scroll', hideMenu, true);
  document.addEventListener('keydown', (ev) => {
    if (ev.key !== 'Escape') return;
    hideMenu();
    if (state.creatingParent !== null) {
      state.creatingParent = null;
      renderAdminTree();
    }
    if (state.renamingFolder !== null) {
      state.renamingFolder = null;
      renderAdminTree();
    }
    if (state.editingPage !== null) {
      state.editingPage = null;
      renderPages();
    }
  });

  loadTree()
    .then(() => selectFolder(1))
    .catch((err) => showNotice($('pagesNotice'), err.message, 'err'));
}

document.addEventListener('DOMContentLoaded', init);
