'use strict';

const $ = (sel) => document.querySelector(sel);
const state = {
  forumId: 0,
  topicId: 0,
  tracks: [],
  topic: null,
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

async function search(q) {
  const box = $('#results');
  const head = $('#results-head');
  q = (q === undefined ? $('#search-input').value : q).trim();
  head.textContent = q ? 'Поиск: ' + q : (state.forumId ? 'Раздел ' + state.forumId : 'Введите запрос');
  box.innerHTML = '<div class="meta">Поиск…</div>';
  try {
    const topics = await api('search?q=' + encodeURIComponent(q) + '&forum=' + state.forumId);
    box.innerHTML = '';
    if (!topics.length) {
      box.innerHTML = '<div class="meta">Ничего не найдено</div>';
      return;
    }
    for (const t of topics) {
      const el = document.createElement('div');
      el.className = 'item';
      el.innerHTML =
        '<span class="t">' + esc(t.title) + '</span>' +
        '<span class="m">' + esc(t.sizeText || human(t.size)) +
        ' · сидов ' + t.seeds + ' · ' + esc(t.category || '') + '</span>';
      el.onclick = () => openTopic(t.id);
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
      (topic.category ? '<br>' + esc(topic.category) : '');

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
  } catch (e) {
    $('#status').textContent = 'нет связи с приложением';
  }
}

// ---------------------------------------------------------------- запуск

$('#search-form').onsubmit = (e) => {
  e.preventDefault();
  search();
};
$('#stop').onclick = stop;

loadSections();
poll();
setInterval(poll, 700);

// Разделы могли не загрузиться из-за сбоя связи — пробуем ещё раз.
setTimeout(() => {
  const box = $('#sections');
  if (box && box.textContent.indexOf('Повторить') >= 0) loadSections();
}, 5000);
