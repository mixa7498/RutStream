'use strict';

const $ = (sel) => document.querySelector(sel);
const state = {
  forumId: 0,
  topicId: 0,
  tracks: [],
  topic: null,
  topics: [],       // сырые результаты поиска (до сортировки/фильтров)
  artist: '',
  sessionOpen: false,
  sessionInit: false,
};

function human(n) {
  n = Number(n) || 0;
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(2) + ' ГБ';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(2) + ' МБ';
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' КБ';
  return n + ' Б';
}

function dur(sec) {
  sec = Number(sec) || 0;
  if (sec <= 0) return '';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return m + ':' + String(s).padStart(2, '0');
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"]/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

async function api(path, opts) {
  const res = await fetch('/api/' + path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok || (data && data.error)) throw new Error((data && data.error) || res.statusText);
  return data;
}

// ---------------------------------------------------------------- разделы

async function loadSections() {
  const box = $('#sections');
  try {
    const forums = await api('sections');
    box.innerHTML = '';
    if (!forums.length) {
      box.innerHTML = '<div class="meta">Разделы не найдены</div>';
      return;
    }
    // Разделы приходят плоским списком с категорией — группируем по ней.
    const cats = new Map();
    for (const f of forums) {
      const key = f.catTitle || 'Музыка';
      if (!cats.has(key)) cats.set(key, []);
      cats.get(key).push(f);
    }
    for (const [cat, items] of cats) {
      const head = document.createElement('div');
      head.className = 'results-head';
      head.textContent = cat;
      box.appendChild(head);
      for (const f of items) {
        const el = document.createElement('div');
        el.className = 'forum';
        el.textContent = f.title;
        el.onclick = () => {
          state.forumId = f.id;
          $('#search-input').value = '';
          search('');
        };
        box.appendChild(el);
      }
    }
  } catch (e) {
    box.innerHTML = '<div class="err">' + esc(e.message) + '</div>' +
      '<button id="sections-retry" style="margin-top:8px">Повторить</button>';
    const b = $('#sections-retry');
    if (b) b.onclick = loadSections;
  }
}

// ---------------------------------------------------------------- поиск

// qualityBadge определяет бейдж качества по названию раздачи.
function qualityBadge(title) {
  const t = (title || '').toLowerCase();
  if (/\b(flac|alac|ape|wav|dsd)\b/.test(t)) {
    if (/24\/?(96|192)|24 ?bit|hi-?res|96\/24|192\/24/.test(t)) return { text: '24/96', cls: 'hi' };
    if (/24\/?48/.test(t)) return { text: '24/48', cls: 'hi' };
    if (/\b(alac)\b/.test(t)) return { text: 'ALAC', cls: 'lossless' };
    if (/\b(ape|wav|dsd)\b/.test(t)) return { text: t.match(/\b(ape|wav|dsd)\b/)[1].toUpperCase(), cls: 'lossless' };
    return { text: 'FLAC', cls: 'lossless' };
  }
  if (/\bv0\b/.test(t)) return { text: 'MP3 V0' };
  if (/320/.test(t)) return { text: 'MP3 320' };
  if (/128/.test(t)) return { text: 'MP3 128' };
  if (/\baac\b/.test(t)) return { text: 'AAC' };
  return null;
}

// isCollection — сборник дискографии.
function isCollection(title) {
  return /дискографи|discograph|collection|полное собрание|studio albums|anthology|box ?set/i.test(title || '');
}

// applyFiltersSort применяет фильтры и сортировку к списку раздач.
function applyFiltersSort() {
  const box = $('#results');
  let list = state.topics.slice();
  if ($('#f-lossless').checked) {
    list = list.filter((t) => /flac|alac|ape|wav|dsd|lossless|hi-?res/i.test(t.title + ' ' + (t.format || '')));
  }
  if ($('#f-seeded').checked) {
    list = list.filter((t) => t.seeds > 0);
  }
  if ($('#f-collections').checked) {
    list = list.filter((t) => isCollection(t.title));
  }
  const sort = $('#sort').value;
  if (sort === 'seeds') list.sort((a, b) => (b.seeds || 0) - (a.seeds || 0));
  else if (sort === 'size') list.sort((a, b) => (b.size || 0) - (a.size || 0));
  else if (sort === 'date') list.sort((a, b) => (b.id || 0) - (a.id || 0));
  else list.sort((a, b) => (b.rating || 0) - (a.rating || 0));

  box.innerHTML = '';
  if (!list.length) {
    box.innerHTML = '<div class="meta">Ничего не найдено</div>';
    return;
  }
  for (const t of list) {
    const el = document.createElement('div');
    el.className = 'item';
    const badge = qualityBadge(t.title + ' ' + (t.format || ''));
    el.innerHTML =
      '<span class="t">' + esc(t.title) +
      (badge ? '<span class="badge ' + badge.cls + '">' + badge.text + '</span>' : '') + '</span>' +
      '<span class="m">' + esc(t.sizeText || human(t.size)) +
      ' · сидов ' + t.seeds + ' · ' + esc(t.category || '') + '</span>';
    el.onclick = () => openTopic(t.id);
    box.appendChild(el);
  }
}

async function search(q) {
  const box = $('#results');
  const head = $('#results-head');
  q = (q === undefined ? $('#search-input').value : q).trim();
  head.textContent = q ? 'Поиск: ' + q : (state.forumId ? 'Раздел ' + state.forumId : 'Введите запрос');
  box.innerHTML = '<div class="meta">Поиск…</div>';
  try {
    const topics = await api('search?q=' + encodeURIComponent(q) + '&forum=' + state.forumId);
    state.topics = topics || [];
    applyFiltersSort();
  } catch (e) {
    box.innerHTML = '<div class="err">' + esc(e.message) + '</div>';
  }
}

// ---------------------------------------------------------------- исполнитель

async function openArtist(name) {
  const box = $('#results');
  const head = $('#results-head');
  state.artist = name;
  head.textContent = 'Исполнитель: ' + name;
  box.innerHTML = '<div class="meta">Поиск раздач исполнителя…</div>';
  try {
    const releases = await api('artist?name=' + encodeURIComponent(name));
    box.innerHTML = '';
    if (!releases.length) {
      box.innerHTML = '<div class="meta">Раздач не найдено</div>';
      return;
    }
    for (const r of releases) {
      const el = document.createElement('div');
      el.className = 'release';
      const topics = (r.topics || []).slice(0, 4);
      el.innerHTML =
        '<div class="t">' + esc(r.album || r.artist) + (r.year ? ' · ' + r.year : '') + '</div>' +
        '<div class="m">' + topics.length + ' раздач' +
        (r.lossless ? ' · lossless' : '') + (r.isCollection ? ' · коллекция' : '') + '</div>' +
        '<div class="act">' + topics.map((t, i) =>
          '<button data-i="' + i + '">' + esc(t.title.slice(0, 26)) + '…</button>').join('') + '</div>';
      el.querySelectorAll('button').forEach((b) => {
        b.onclick = () => openTopic(topics[Number(b.dataset.i)].id);
      });
      box.appendChild(el);
    }
  } catch (e) {
    box.innerHTML = '<div class="err">' + esc(e.message) + '</div>';
  }
}

// ---------------------------------------------------------------- тема

async function openTopic(id) {
  state.topicId = id;
  const head = $('#tracks-head');
  const box = $('#tracks');
  head.hidden = false;
  box.hidden = false;
  head.textContent = 'Треклист';
  box.innerHTML = '<div class="meta">Загрузка…</div>';
  try {
    const topic = await api('topic?id=' + id);
    state.topic = topic;
    $('#release').innerHTML =
      '<b>' + esc(topic.title) + '</b><br>' +
      esc(topic.sizeText || human(topic.size)) + ' · сидов ' + topic.seeds +
      (topic.category ? '<br>' + esc(topic.category) : '') +
      '<div class="act">' +
      '<button id="dl-album">Скачать альбом</button>' +
      (topic.artist ? '<button id="artist-open">Исполнитель</button>' : '') +
      '</div>';
    $('#dl-album').onclick = () => download('album');
    const ab = $('#artist-open');
    if (ab) {
      ab.textContent = topic.artist;
      ab.onclick = () => openArtist(topic.artist);
    }
    loadSimilar(id);

    const plan = await api('plan?id=' + id);
    state.tracks = plan.tracks || [];
    box.innerHTML = '';
    if (!state.tracks.length) {
      box.innerHTML = '<div class="meta">Треки не найдены</div>';
      return;
    }
    head.textContent = 'Треклист · раздача ' + human(plan.total);
    for (const t of state.tracks) {
      const el = document.createElement('div');
      el.className = 'track';
      el.innerHTML =
        '<span class="no">' + t.no + '</span>' +
        '<span class="name" title="' + esc(t.position) + '">' + esc(t.title) + '</span>' +
        '<span class="len">' + (dur(t.duration) || human(t.size)) + '</span>' +
        '<span class="act">' +
        '<button data-act="play" title="Слушать">▶</button>' +
        '<button data-act="dl" title="Скачать">⬇</button>' +
        '</span>';
      el.querySelector('[data-act=play]').onclick = (ev) => {
        ev.stopPropagation();
        play(t.no);
      };
      el.querySelector('[data-act=dl]').onclick = (ev) => {
        ev.stopPropagation();
        download(t.no);
      };
      box.appendChild(el);
    }
  } catch (e) {
    box.innerHTML = '<div class="err">' + esc(e.message) + '</div>';
  }
}

// ---------------------------------------------------------------- действия

async function play(track) {
  try {
    await api('play', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: state.topicId, track: track }),
    });
    poll();
  } catch (e) {
    $('#player').innerHTML = '<div class="err">' + esc(e.message) + '</div>';
  }
}

async function stop() {
  try {
    await api('stop', { method: 'POST' });
  } catch (e) { /* не критично */ }
  poll();
}

async function download(what) {
  try {
    await api('download', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: state.topicId, what: String(what) }),
    });
    poll();
  } catch (e) {
    alert('Не удалось поставить в очередь: ' + e.message);
  }
}

// ---------------------------------------------------------------- рекомендации

// loadSimilar показывает популярных исполнителей раздела темы.
async function loadSimilar(topicId) {
  const box = $('#similar');
  if (!topicId) {
    box.innerHTML = '<div class="meta">—</div>';
    return;
  }
  try {
    const list = await api('similar?topic=' + topicId + '&limit=10');
    box.innerHTML = '';
    if (!list || !list.length) {
      box.innerHTML = '<div class="meta">—</div>';
      return;
    }
    for (const a of list) {
      const el = document.createElement('div');
      el.className = 'forum';
      el.textContent = a.artist + (a.lossless ? ' · lossless' : '') + ' (' + a.count + ')';
      el.onclick = () => {
        $('#search-input').value = a.artist;
        search(a.artist);
      };
      box.appendChild(el);
    }
  } catch (e) {
    box.innerHTML = '<div class="meta">—</div>';
  }
}

// loadHistory показывает недавно слушанные треки.
async function loadHistory() {
  const box = $('#history');
  try {
    const list = await api('history?limit=10');
    box.innerHTML = '';
    if (!list || !list.length) {
      box.innerHTML = '<div class="meta">—</div>';
      return;
    }
    for (const h of list) {
      const el = document.createElement('div');
      el.className = 'forum';
      el.textContent = (h.title || h.trackId) + (h.artist ? ' — ' + h.artist : '');
      el.title = h.trackId;
      el.onclick = () => {
        const [id, no] = String(h.trackId).split(':');
        if (id && no) openTopic(Number(id));
      };
      box.appendChild(el);
    }
  } catch (e) {
    box.innerHTML = '<div class="meta">—</div>';
  }
}

// ---------------------------------------------------------------- статус

async function poll() {
  try {
    const st = await api('state');
    const p = st.playing || {};
    const box = $('#player');
    const title = p.title || 'Ничего не играет';
    const meta = [];
    if (p.state === 'preparing') meta.push('подготовка…');
    if (p.state === 'playing') meta.push('играет · ' + dur(p.elapsedSec));
    if (p.state === 'done') meta.push('окончено');
    if (p.state === 'error') meta.push('ошибка: ' + p.error);
    if (p.bytesDownloaded) meta.push('скачано ' + human(p.bytesDownloaded));
    box.querySelector('.player-title').textContent = title;
    box.querySelector('.player-meta').textContent = meta.join(' · ');
    const fill = box.querySelector('.bar-fill');
    const total = (state.tracks[p.track - 1] || {}).duration || 0;
    let pct = 0;
    if (p.totalBytes && p.bytesDownloaded) {
      pct = Math.min(100, 100 * p.bytesDownloaded / p.totalBytes);
    } else if (total && p.elapsedSec) {
      pct = Math.min(100, 100 * p.elapsedSec / total);
    }
    fill.style.width = pct + '%';
    $('#stop').disabled = p.state !== 'playing' && p.state !== 'preparing';

    const dl = $('#downloads');
    const list = st.downloads || [];
    if (!list.length) {
      dl.innerHTML = '<div class="meta">Очередь пуста</div>';
    } else {
      dl.innerHTML = '';
      for (const d of list) {
        const el = document.createElement('div');
        el.className = 'dl ' + d.state;
        const pr = d.bytesTotal ? Math.round(100 * d.bytesDone / d.bytesTotal) : 0;
        el.innerHTML =
          '<div class="t">' + esc(d.title) + '</div>' +
          '<div class="m">' + d.state + ' · ' + human(d.bytesDone) + ' / ' + human(d.bytesTotal) + '</div>' +
          '<div class="bar"><div class="bar-fill" style="width:' + pr + '%"></div></div>';
        dl.appendChild(el);
      }
    }
    $('#status').textContent = st.loggedIn ? 'сессия активна' : 'нет авторизации';
    // Поле ввода cookie показываем автоматически, пока авторизации нет.
    // Кнопка «Сессия» позволяет открыть его в любой момент.
    if (!state.sessionInit) {
      state.sessionOpen = !st.loggedIn;
      state.sessionInit = true;
    }
    const bar = $('#session-bar');
    if (bar) bar.hidden = !!st.loggedIn && !state.sessionOpen;

    // Рейтинг раздачи (Ratio Guard).
    const r = st.ratio || {};
    const rbox = $('#ratio');
    if (r.downloaded > 0) {
      rbox.innerHTML =
        'скачано <b>' + human(r.downloaded) + '</b> · раздано <b>' + human(r.uploaded) + '</b> · ' +
        'рейтинг <b>' + (r.value || 0).toFixed(2) + '</b>' +
        (r.min ? ' (минимум ' + r.min.toFixed(2) + ')' : '') +
        (r.warn ? '<br><span class="err">Рейтинг ниже нормы' +
          (r.need ? ': нужно отдать ещё ' + human(r.need) : '') +
          '. Держите раздачу включённой или докачайте её.</span>' : '');
    } else {
      rbox.innerHTML = '';
    }
  } catch (e) {
    $('#status').textContent = 'нет связи с приложением';
  }
}

// ---------------------------------------------------------------- сессия

async function saveSession() {
  const value = $('#session-input').value.trim();
  const msg = $('#session-msg');
  if (!value) {
    msg.textContent = 'Вставьте строку cookie (document.cookie)';
    msg.className = 'session-msg err';
    return;
  }
  msg.textContent = 'Сохраняем…';
  msg.className = 'session-msg';
  try {
    const r = await api('session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cookie: value }),
    });
    msg.textContent = r.hasClearance
      ? 'Сохранено: сессия и cookie Cloudflare'
      : 'Сохранено. Совет: копируйте всю строку document.cookie — в ней есть cf_clearance.';
    msg.className = 'session-msg ok';
    $('#session-input').value = '';
    state.sessionOpen = false;
    $('#session-bar').hidden = true;
    poll();
  } catch (e) {
    msg.textContent = e.message;
    msg.className = 'session-msg err';
  }
}

async function clearSession() {
  const msg = $('#session-msg');
  try {
    await api('session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ clear: true }),
    });
    msg.textContent = 'Сессия удалена';
    msg.className = 'session-msg';
    poll();
  } catch (e) {
    msg.textContent = e.message;
    msg.className = 'session-msg err';
  }
}

// ---------------------------------------------------------------- запуск

$('#search-form').onsubmit = (e) => {
  e.preventDefault();
  search();
};
$('#stop').onclick = stop;
$('#session-save').onclick = saveSession;
$('#session-clear').onclick = clearSession;
$('#session-input').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') saveSession();
});
$('#session-btn').onclick = () => {
  state.sessionOpen = !state.sessionOpen;
  $('#session-bar').hidden = !state.sessionOpen;
  if (state.sessionOpen) $('#session-input').focus();
};
$('#help').onclick = () => { $('#help-modal').hidden = false; };
$('#help-close').onclick = () => {
  $('#help-modal').hidden = true;
  try { localStorage.setItem('rutstream-help-seen', '1'); } catch (e) { /* нет доступа */ }
};
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    $('#help-modal').hidden = true;
    state.sessionOpen = false;
    $('#session-bar').hidden = true;
  }
});
// Первый запуск: показываем справку сразу.
try {
  if (!localStorage.getItem('rutstream-help-seen')) $('#help-modal').hidden = false;
} catch (e) { /* нет доступа */ }
$('#sort').onchange = applyFiltersSort;
['#f-lossless', '#f-seeded', '#f-collections'].forEach((sel) => {
  $(sel).onchange = applyFiltersSort;
});

loadSections();
poll();
loadHistory();
setInterval(poll, 700);
setInterval(loadHistory, 15000);

// Разделы могли не загрузиться из-за сбоя связи — пробуем ещё раз.
setTimeout(() => {
  const box = $('#sections');
  if (box && box.textContent.indexOf('Повторить') >= 0) loadSections();
}, 5000);
