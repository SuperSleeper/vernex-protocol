/* game.js — Vernex text adventure SPA
   Served as /static/game.js; loaded at end of <body> in the /game template.
   All game context data fetched from /api/game/contexts (no inline injection). */

// ── Game data (fetched on init) ───────────────────────────────
var GAME_DATA = null;

// ── Constants ─────────────────────────────────────────────────
var STAT_NAMES = {
  fantasy: ['Strength','Stamina','Charisma','Magic','Agility','Luck'],
  scifi:   ['Intelligence','Tech Skill','Agility','Charisma','Endurance','Luck'],
  action:  ['Strength','Stamina','Charisma','Cunning','Agility','Luck'],
  comedy:  ['Charisma','Wit','Luck','Clumsiness','Charm','Stubbornness']
};
var SUBTYPES = {
  fantasy: {lbl:'Class',    byG:true,  opts:{male:['Warrior','Elf Archer','Wizard','Thief'], female:['Valkyrie','Elf Archer','Wizard','Thief']}},
  scifi:   {lbl:'Scenario', byG:false, opts:['AI','Aliens','Space Travel','Time Travel']},
  action:  {lbl:'Era',      byG:false, opts:['Egyptian Pharaoh Era','Roman Empire','Renaissance','American Wild West','World War II']},
  comedy:  {lbl:'Subtype',  byG:false, opts:['Workplace Comedy','Small Town Chaos','Royally Confused','Superhero Farce']}
};
var STAT_MODIFIERS = {
  fantasy: {
    'Warrior':    {Strength:2,  Stamina:2,  Charisma:-2, Magic:-2, Agility:0,  Luck:0},
    'Valkyrie':   {Strength:2,  Stamina:2,  Charisma:-2, Magic:-2, Agility:0,  Luck:0},
    'Elf Archer': {Strength:-2, Stamina:0,  Charisma:0,  Magic:0,  Agility:2,  Luck:2},
    'Wizard':     {Strength:-2, Stamina:-2, Charisma:0,  Magic:2,  Agility:0,  Luck:2},
    'Thief':      {Strength:-2, Stamina:0,  Charisma:2,  Magic:-2, Agility:2,  Luck:2}
  },
  scifi: {
    'AI':           {'Intelligence':2,  'Tech Skill':2,  Agility:-2, Charisma:-2, Endurance:0,  Luck:0},
    'Aliens':       {'Intelligence':0,  'Tech Skill':-2, Agility:2,  Charisma:0,  Endurance:2,  Luck:-2},
    'Space Travel': {'Intelligence':0,  'Tech Skill':2,  Agility:0,  Charisma:0,  Endurance:2,  Luck:-2},
    'Time Travel':  {'Intelligence':2,  'Tech Skill':2,  Agility:0,  Charisma:-2, Endurance:-2, Luck:0}
  },
  action: {
    'Egyptian Pharaoh Era': {Strength:0,  Stamina:2,  Charisma:2,  Cunning:0,  Agility:-2, Luck:-2},
    'Roman Empire':         {Strength:2,  Stamina:2,  Charisma:0,  Cunning:-2, Agility:0,  Luck:-2},
    'Renaissance':          {Strength:-2, Stamina:0,  Charisma:2,  Cunning:2,  Agility:0,  Luck:-2},
    'American Wild West':   {Strength:0,  Stamina:0,  Charisma:2,  Cunning:2,  Agility:-2, Luck:-2},
    'World War II':         {Strength:2,  Stamina:2,  Charisma:-2, Cunning:2,  Agility:0,  Luck:-2}
  },
  comedy: {
    'Workplace Comedy': {Charisma:0,  Wit:2,  Luck:-2, Clumsiness:2,  Charm:0,  Stubbornness:-2},
    'Small Town Chaos': {Charisma:2,  Wit:0,  Luck:-2, Clumsiness:2,  Charm:-2, Stubbornness:0},
    'Royally Confused': {Charisma:2,  Wit:2,  Luck:0,  Clumsiness:-2, Charm:0,  Stubbornness:-2},
    'Superhero Farce':  {Charisma:-2, Wit:2,  Luck:2,  Clumsiness:0,  Charm:-2, Stubbornness:0}
  }
};

// ── State ─────────────────────────────────────────────────────
var _genre = null, _rolledStats = null, _character = null;
var _history = [], _gameStarted = false, _currentSaveId = null, _turnCount = 0;

// ── View management ───────────────────────────────────────────
function showView(v) {
  ['view-select','view-create','view-play'].forEach(function(id) {
    document.getElementById(id).style.display = (id === v) ? 'block' : 'none';
  });
  adjustPadding();
}

function adjustPadding() {
  var hdr = document.getElementById('site-hdr');
  if (!hdr) return;
  var h = hdr.offsetHeight;
  document.querySelectorAll('.view-main').forEach(function(el) {
    el.style.paddingTop = (h + 14) + 'px';
  });
}

// ── Genre selection ───────────────────────────────────────────
function selectGenre(g) {
  _genre = g;
  var lbl = {fantasy:'Fantasy Adventure', scifi:'Science Fiction', action:'Action / Adventure', comedy:'Comedic Drama'};
  document.getElementById('cc-genre-lbl').textContent = lbl[g] || g;
  renderSubtypeOpts();
  document.getElementById('stat-block').innerHTML = '<p class="stat-ph">Click “Roll Character” to generate stats.</p>';
  document.getElementById('start-game-btn').disabled = true;
  document.getElementById('game-context').value = '';
  _rolledStats = null;
  loadSavedPrompt();
  showView('view-create');
}

function backToSelect() { _genre = null; showView('view-select'); }

// ── Character creator ─────────────────────────────────────────
function getGender() {
  var el = document.querySelector('input[name="gender"]:checked');
  return el ? el.value : 'male';
}

function onGenderChange() { if (_genre === 'fantasy') renderSubtypeOpts(); }

function getSubtype() {
  var el = document.querySelector('input[name="subtype"]:checked');
  return el ? el.value : '';
}

function renderSubtypeOpts() {
  var st = SUBTYPES[_genre];
  var opts = st.byG ? st.opts[getGender()] : st.opts;
  document.getElementById('subtype-lbl').textContent = st.lbl;
  document.getElementById('subtype-opts').innerHTML = opts.map(function(o, i) {
    return '<label class="radio-label"><input type="radio" name="subtype" value="' + o + '"'
      + (i === 0 ? ' checked' : '') + '>' + o + '</label>';
  }).join('');
}

function roll4d6() {
  var d = [0,0,0,0].map(function() { return Math.floor(Math.random() * 6) + 1; });
  d.sort(function(a, b) { return a - b; });
  return d[1] + d[2] + d[3];
}

function statBar(v) {
  var f = Math.min(8, Math.max(0, Math.round((v - 3) / 15 * 8)));
  return '█'.repeat(f) + '░'.repeat(8 - f);
}

function rollCharacter() {
  var names = STAT_NAMES[_genre];
  var mods = (STAT_MODIFIERS[_genre] || {})[getSubtype()] || {};
  _rolledStats = {};
  var html = '';
  names.forEach(function(n) {
    var raw = roll4d6();
    var mod = mods[n] !== undefined ? mods[n] : 0;
    _rolledStats[n] = Math.min(20, Math.max(1, raw + mod));
    var modStr = mod > 0 ? ' <span style="color:#3fb950;font-size:.75rem">(+' + mod + ')</span>'
               : mod < 0 ? ' <span style="color:#f85149;font-size:.75rem">(' + mod + ')</span>'
               : '';
    html += '<div class="stat-row">'
      + '<span class="stat-name">' + n + '</span>'
      + '<span class="stat-bar">' + statBar(_rolledStats[n]) + '</span>'
      + '<span class="stat-val">&nbsp;' + _rolledStats[n] + modStr + '</span>'
      + '</div>';
  });
  document.getElementById('stat-block').innerHTML = html;
  if (!document.getElementById('game-context').value.trim()) {
    document.getElementById('game-context').value = buildContext();
  }
  document.getElementById('start-game-btn').disabled = false;
}

// ── Prompt persistence ────────────────────────────────────────
function loadSavedPrompt() {
  fetch('/api/game/prompts/' + _genre)
    .then(function(r) { return r.json(); })
    .then(function(d) { if (d.prompt) document.getElementById('game-context').value = d.prompt; })
    .catch(function() {});
}

function savePromptDefault() {
  var ctx = document.getElementById('game-context').value.trim();
  if (!ctx) { alert('Roll character first to generate a context.'); return; }
  fetch('/api/game/prompts', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({genre: _genre, prompt: ctx})
  }).then(function() { showCtxStatus('✓ Saved as default'); })
    .catch(function() { showCtxStatus('Save failed'); });
}

function resetPromptDefault() {
  fetch('/api/game/prompts', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({genre: _genre, prompt: null})
  }).then(function() {
    document.getElementById('game-context').value = _rolledStats ? buildContext() : '';
    showCtxStatus('Reset to default');
  }).catch(function() {});
}

function showCtxStatus(m) {
  var e = document.getElementById('ctx-st');
  e.textContent = m;
  setTimeout(function() { e.textContent = ''; }, 2500);
}

// ── Context generation ────────────────────────────────────────
function sbLines() {
  return Object.keys(_rolledStats).map(function(n) {
    return (n + '              ').slice(0, 14) + statBar(_rolledStats[n]) + ' ' + _rolledStats[n];
  }).join('\n');
}

function buildContext() {
  if (!GAME_DATA) return '';
  var name = document.getElementById('char-name').value.trim() || 'the adventurer';
  var gender = getGender(), sub = getSubtype();
  var pr = gender === 'male' ? 'He' : 'She';
  var sb = sbLines(), ls = GAME_DATA.levelSystem;
  var ch = 'CHARACTER\nName:   ' + name + '\nGender: ' + gender + '\n';

  if (_genre === 'fantasy') {
    ch += 'Class:  ' + sub + '\n';
    return 'You are a dynamic, adaptive text-adventure game engine set in a classic fantasy world.\n'
      + 'Never break character or explain your mechanics.\n\n'
      + ch + '\nSTAT BLOCK\n' + sb + '\n\n' + ls
      + '\n\n### 4. GENRE MECHANICS\nSpells, potions, enchanted items. MAGIC governs spell power; '
      + 'STRENGTH melee; AGILITY stealth. Track inventory (max 6 items). Medieval fantasy — no modern technology.\n\n'
      + '### 5. OPENING MOVE\nBegin at Level 1. ' + name + ' (' + sub + ') wakes '
      + GAME_DATA.openings.fantasy + '.';
  }

  if (_genre === 'scifi') {
    ch += 'Scenario: ' + sub + '\n';
    var scifiOp = GAME_DATA.openings.scifi[sub] || 'waking in an unfamiliar technological environment';
    return 'You are a dynamic, adaptive text-adventure game engine set in a science fiction universe.\n'
      + 'Never break character or explain your mechanics.\n\n'
      + ch + '\nSTAT BLOCK\n' + sb + '\n\n' + ls
      + '\n\n### 4. GENRE MECHANICS\nTechnology, science, gadgets. INTELLIGENCE governs problem-solving; '
      + 'TECH SKILL device operation. Track equipment (max 6). No magic — plausible science only.\n\n'
      + '### 5. OPENING MOVE\nBegin at Level 1. ' + name + ' (' + sub + ' scenario): ' + pr + ' is ' + scifiOp + '.';
  }

  if (_genre === 'comedy') {
    ch += 'Subtype: ' + sub + '\n';
    var comedyOp = (GAME_DATA.openings.comedy || {})[sub] || 'finding themselves in an absurd situation';
    return 'You are a dynamic, adaptive text-adventure game engine set in a lighthearted comedic drama world.\n'
      + 'Never break character or explain your mechanics. Tone is lighthearted situational comedy — no dark themes, PG-13 only.\n\n'
      + ch + '\nSTAT BLOCK\n' + sb + '\n\n' + ls
      + '\n\n### 4. GENRE MECHANICS\nLighthearted comedic drama. WIT governs wordplay and clever solutions; '
      + 'CHARISMA social interactions; LUCK random fortune; CLUMSINESS causes comic mishaps (higher score = more frequent mishaps). '
      + 'Replace the Status line every response with "Embarrassment Meter: [Low/Medium/High/Critical]". '
      + 'Level escalation leads to increasingly absurd situations rather than danger. '
      + '1% chance any response breaks into a short musical number.\n\n'
      + '### 5. OPENING MOVE\nBegin at Level 1. ' + name + ' (' + sub + '): ' + pr + ' is ' + comedyOp + '.';
  }

  // action
  ch += 'Era:    ' + sub + '\n';
  var actionOp = GAME_DATA.openings.action[sub] || 'finding themselves at the heart of a historical moment';
  return 'You are a dynamic, adaptive text-adventure game engine set in a historical action-adventure world.\n'
    + 'Never break character or explain your mechanics.\n\n'
    + ch + '\nSTAT BLOCK\n' + sb + '\n\n' + ls
    + '\n\n### 4. GENRE MECHANICS\nCombat, diplomacy, survival. All items and language MUST be era-accurate for "'
    + sub + '". STRENGTH governs physical; CUNNING strategy/deception; CHARISMA social. '
    + 'Track equipment (max 6). PG-13 only.\n\n'
    + '### 5. OPENING MOVE\nBegin at Level 1. ' + name + ' in the ' + sub + ' era: ' + pr + ' is ' + actionOp + '.';
}

// ── Game start ────────────────────────────────────────────────
async function startGame() {
  if (!_rolledStats) { alert('Roll your character first.'); return; }
  var name = document.getElementById('char-name').value.trim() || 'the adventurer';
  _character = {
    genre: _genre, name: name, gender: getGender(),
    subtype: getSubtype(), stats: _rolledStats
  };
  var ctx = document.getElementById('game-context').value.trim() || buildContext();
  renderCharSheet();
  showView('view-play');
  _history = [{role:'system', content:ctx}, {role:'user', content:'Begin the adventure.'}];
  var log = document.getElementById('chat-log');
  log.innerHTML = '';
  document.getElementById('prompt').disabled = true;
  document.getElementById('submit-btn').disabled = true;
  document.getElementById('qs-btn').disabled = true;
  _turnCount = 0; _currentSaveId = null; _gameStarted = false;
  try {
    var resp = await gameFetch(_history, document.getElementById('model-select').value);
    _history.push({role:'assistant', content:resp});
    appendMsg('assistant', resp);
    _gameStarted = true;
    document.getElementById('prompt').disabled = false;
    document.getElementById('submit-btn').disabled = false;
    document.getElementById('qs-btn').disabled = false;
    document.getElementById('prompt').focus();
  } catch(err) {
    appendMsg('error', 'Failed to start: ' + err.message);
  }
}

function renderCharSheet() {
  document.getElementById('cs-name').textContent = _character.name + ' · ' + _character.subtype;
  document.getElementById('cs-inner').innerHTML = Object.keys(_character.stats).map(function(n) {
    return '<div class="stat-row">'
      + '<span class="stat-name">' + n + '</span>'
      + '<span class="stat-bar">' + statBar(_character.stats[n]) + '</span>'
      + '<span class="stat-val">&nbsp;' + _character.stats[n] + '</span>'
      + '</div>';
  }).join('');
}

// ── Chat ──────────────────────────────────────────────────────
async function sendTurn(e) {
  if (e && e.preventDefault) e.preventDefault();
  if (!_gameStarted) return;
  var prompt = document.getElementById('prompt').value.trim();
  if (!prompt) return;
  var btn = document.getElementById('submit-btn');
  var model = document.getElementById('model-select').value;
  _history.push({role:'user', content:prompt});
  appendMsg('user', prompt);
  document.getElementById('prompt').value = '';
  btn.disabled = true; btn.textContent = '...';
  try {
    var resp = await gameFetch(_history, model);
    _history.push({role:'assistant', content:resp});
    appendMsg('assistant', resp);
    _turnCount++;
    if (_turnCount % 5 === 0) autoSave();
  } catch(err) {
    _history.pop();
    appendMsg('error', 'Request failed: ' + err.message);
  } finally {
    btn.disabled = false; btn.textContent = 'Send';
  }
}

async function gameFetch(msgs, model) {
  var r = await fetch('/api/game/chat', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({messages: msgs, model: model})
  });
  var d = await r.json();
  if (d.error && !d.response) throw new Error(d.error);
  return d.response || d.error || JSON.stringify(d);
}

function appendMsg(role, content) {
  var log = document.getElementById('chat-log');
  var div = document.createElement('div');
  div.className = 'msg ' + role;
  if (role === 'assistant') {
    div.innerHTML = (typeof marked !== 'undefined')
      ? marked.parse(content, {breaks:true, gfm:true})
      : content.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  } else {
    div.textContent = content;
  }
  log.appendChild(div);
  requestAnimationFrame(function() { log.scrollTop = log.scrollHeight; });
}

function resetGame() {
  _history = []; _gameStarted = false; _character = null; _rolledStats = null;
  _genre = null; _currentSaveId = null; _turnCount = 0;
  document.getElementById('prompt').disabled = true;
  document.getElementById('submit-btn').disabled = true;
  document.getElementById('prompt').value = '';
  document.getElementById('chat-log').innerHTML = '';
  hideSaveForm(); hideLoadPanel();
  showView('view-select');
  loadSelectionSaves();
}

// ── Save / Load ───────────────────────────────────────────────
function toggleSaveForm() {
  var f = document.getElementById('sv-form');
  if (f.style.display === 'none' || !f.style.display) {
    f.style.display = 'flex';
    document.getElementById('sv-name-inp').focus();
  } else { hideSaveForm(); }
}

function hideSaveForm() { document.getElementById('sv-form').style.display = 'none'; }

async function confirmSave() {
  var name = document.getElementById('sv-name-inp').value.trim();
  if (!name) { alert('Enter a save name.'); return; }
  await saveGame(name, null);
  hideSaveForm();
  document.getElementById('sv-name-inp').value = '';
}

async function saveGame(name, id) {
  if (!_character) return;
  var pay = {
    save_name: name, genre: _character.genre, subtype: _character.subtype,
    char_name: _character.name, gender: _character.gender,
    stats: _character.stats, history: _history
  };
  try {
    var url = id ? '/api/game/saves/' + id : '/api/game/saves';
    var method = id ? 'PUT' : 'POST';
    var r = await fetch(url, {method: method, headers: {'Content-Type':'application/json'}, body: JSON.stringify(pay)});
    var d = await r.json();
    if (d.save_id) _currentSaveId = d.save_id;
    setSvStatus('✓ Saved: ' + name);
  } catch(err) { setSvStatus('Save failed'); }
}

async function quickSave() {
  if (!_character) return;
  await saveGame('Quicksave — ' + _character.name, _currentSaveId || null);
}

async function autoSave() {
  if (!_character) return;
  var pay = {
    save_name: 'Autosave', genre: _character.genre, subtype: _character.subtype,
    char_name: _character.name, gender: _character.gender,
    stats: _character.stats, history: _history, autosave: true
  };
  try {
    await fetch('/api/game/saves/autosave-' + _character.genre, {
      method: 'PUT', headers: {'Content-Type':'application/json'}, body: JSON.stringify(pay)
    });
  } catch(e) {}
}

function setSvStatus(m) {
  var e = document.getElementById('sv-st');
  e.textContent = m;
  setTimeout(function() { e.textContent = ''; }, 3000);
}

function toggleLoadPanel() {
  var p = document.getElementById('load-panel');
  if (p.style.display === 'none' || !p.style.display) { refreshSaveList(); p.style.display = 'block'; }
  else hideLoadPanel();
}

function hideLoadPanel() { document.getElementById('load-panel').style.display = 'none'; }

async function refreshSaveList() {
  var list = document.getElementById('load-list');
  list.innerHTML = '<div class="load-empty">Loading…</div>';
  try {
    var saves = await fetch('/api/game/saves').then(function(r) { return r.json(); });
    if (!saves.length) { list.innerHTML = '<div class="load-empty">No saves found.</div>'; return; }
    list.innerHTML = saves.map(function(s) {
      var dt = s.updated_at ? s.updated_at.slice(0, 16).replace('T', ' ') : '';
      return '<div class="load-item" data-save-id="' + s.save_id + '">'
        + '<div class="load-item-info">'
        + '<span class="li-name">' + s.save_name + '</span>'
        + '<span class="li-meta">' + s.genre + ' · ' + s.char_name + ' · ' + dt + '</span>'
        + '</div>'
        + '<button class="btn-del" data-delete-id="' + s.save_id + '">&#128465;</button>'
        + '</div>';
    }).join('');
  } catch(e) { list.innerHTML = '<div class="load-empty">Failed to load saves.</div>'; }
}

async function loadGame(id) {
  try {
    var s = await fetch('/api/game/saves/' + id).then(function(r) { return r.json(); });
    _character = {genre: s.genre, name: s.char_name, gender: s.gender, subtype: s.subtype, stats: s.stats};
    _history = s.history || []; _currentSaveId = id; _turnCount = 0; _gameStarted = true;
    renderCharSheet();
    showView('view-play');
    hideLoadPanel();
    var log = document.getElementById('chat-log');
    log.innerHTML = '';
    var last = _history.filter(function(m) { return m.role === 'assistant'; });
    if (last.length) appendMsg('assistant', last[last.length - 1].content);
    document.getElementById('prompt').disabled = false;
    document.getElementById('submit-btn').disabled = false;
    document.getElementById('qs-btn').disabled = false;
    setSvStatus('✓ Loaded: ' + s.save_name);
  } catch(err) { alert('Load failed: ' + err.message); }
}

async function deleteGame(id) {
  if (!confirm('Delete this save?')) return;
  try {
    await fetch('/api/game/saves/' + id, {method: 'DELETE'});
    refreshSaveList();
  } catch(err) { alert('Delete failed: ' + err.message); }
}

// ── Selection screen saves ─────────────────────────────────────
function escHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function renderSelSaves(container, saves) {
  if (!saves.length) { container.innerHTML = '<div class="sel-empty">No saved games yet.</div>'; return; }
  container.innerHTML = saves.map(function(s) {
    var dt   = s.updated_at ? s.updated_at.slice(0, 16).replace('T', ' ') : '';
    var meta = [s.genre, s.char_name, s.subtype, dt].filter(Boolean).join(' · ');
    return '<div class="sel-save-item" data-save-id="' + escHtml(s.save_id) + '">'
      + '<div class="sel-save-info">'
      + '<div class="sel-save-name">' + escHtml(s.save_name) + '</div>'
      + '<div class="sel-save-meta">' + escHtml(meta) + '</div>'
      + '</div>'
      + '</div>';
  }).join('');
}

async function loadSelectionSaves() {
  var regularEl = document.getElementById('sel-saves-list');
  var autoEl    = document.getElementById('sel-autosaves-list');
  if (!regularEl) return;
  try {
    var saves = await fetch('/api/game/saves').then(function(r) { return r.json(); });
    var regular = saves.filter(function(s) { return !s.autosave; });
    var autos   = saves.filter(function(s) { return !!s.autosave; });
    renderSelSaves(regularEl, regular);
    if (autoEl) renderSelSaves(autoEl, autos);
  } catch(e) {
    if (regularEl) regularEl.innerHTML = '<div class="sel-empty">Failed to load saves.</div>';
    if (autoEl)    autoEl.innerHTML    = '<div class="sel-empty">—</div>';
  }
}

// ── User identity ─────────────────────────────────────────────
function loadUserInfo() {
  fetch('/api/me', {credentials: 'include'})
    .then(function(r) { return r.ok ? r.json() : null; })
    .then(function(d) {
      if (!d || !d.email) return;
      var hdrEl = document.getElementById('hdr-user');
      if (hdrEl) hdrEl.textContent = d.email;
      var selEl = document.getElementById('sel-user-line');
      if (selEl) selEl.textContent = 'Playing as: ' + d.email;
    })
    .catch(function() {});
}

// ── Models ────────────────────────────────────────────────────
function loadModels() {
  fetch('/api/models').then(function(r) { return r.json(); }).then(function(d) {
    var sel = document.getElementById('model-select');
    if (d.models && d.models.length) {
      sel.innerHTML = '';
      d.models.forEach(function(m) {
        var o = document.createElement('option'); o.value = m; o.textContent = m; sel.appendChild(o);
      });
      var pref = d.models.indexOf('gemma4:e4b') >= 0 ? 'gemma4:e4b' : d.models[0];
      sel.value = pref;
    }
  }).catch(function() {});
}

// ── Node info ─────────────────────────────────────────────────
function loadNodeInfo() {
  fetch('/api/status', {credentials: 'include'})
    .then(function(r) { return r.json(); })
    .then(function(d) {
      document.getElementById('node-info').textContent = (d.node_id || '') + '  ·  v' + (d.version || '');
    })
    .catch(function() { document.getElementById('node-info').textContent = 'status unavailable'; });
}

// ── GPU gauge ─────────────────────────────────────────────────
function initGpuGauges() {
  var GPU_NODES = [{id:'node1', label:'node1 · RTX 3070', url:'/api/gpu'}];
  var container = document.getElementById('gpu-gauges');
  if (!container) return;
  GPU_NODES.forEach(function(n) {
    var c = document.createElement('div');
    c.className = 'gpu-card'; c.id = 'gpu-card-' + n.id;
    c.innerHTML = '<div class="gpu-card-label">' + n.label + '</div>'
      + '<div class="gpu-bar-row">'
      + '<div class="gpu-bar-track"><div class="gpu-bar-fill" id="gpu-bar-' + n.id + '" style="width:0"></div></div>'
      + '<span class="gpu-card-stats" id="gpu-stats-' + n.id + '">—</span>'
      + '</div>';
    container.appendChild(c);
  });
  function pollNode(n) {
    fetch(n.url).then(function(r) { return r.json(); }).then(function(d) {
      var bar   = document.getElementById('gpu-bar-'   + n.id);
      var stats = document.getElementById('gpu-stats-' + n.id);
      var card  = document.getElementById('gpu-card-'  + n.id);
      if (d.error) {
        if (stats) stats.textContent = 'unavailable';
        if (card)  card.className = 'gpu-card';
        return;
      }
      var pct = d.vram_total_mb > 0 ? d.vram_used_mb / d.vram_total_mb * 100 : 0;
      var active = d.gpu_util_pct > 20;
      bar.style.width = pct.toFixed(1) + '%';
      bar.className   = 'gpu-bar-fill' + (active ? ' active' : '');
      card.className  = 'gpu-card'     + (active ? ' active' : '');
      stats.textContent = (d.vram_used_mb / 1024).toFixed(1) + '/' + (d.vram_total_mb / 1024).toFixed(1)
        + ' GB · ' + d.gpu_util_pct + '% · ' + d.temp_c + '°C';
    }).catch(function() {
      var stats = document.getElementById('gpu-stats-' + n.id);
      if (stats) stats.textContent = 'unavailable';
    });
  }
  function poll() { GPU_NODES.forEach(pollNode); }
  poll(); setInterval(poll, 3000);
}

// ── Init ──────────────────────────────────────────────────────
(function init() {
  // Load game context data from server (avoids CSP/injection issues)
  fetch('/api/game/contexts')
    .then(function(r) { return r.json(); })
    .then(function(d) { GAME_DATA = d; })
    .catch(function() { console.warn('[vernex] failed to load game contexts'); });

  // Genre card click handlers (data-genre attribute, no inline onclick)
  document.querySelectorAll('.genre-card[data-genre]').forEach(function(card) {
    card.addEventListener('click', function() { selectGenre(card.getAttribute('data-genre')); });
  });

  // Character creator buttons
  document.getElementById('cc-back-btn').addEventListener('click',   backToSelect);
  document.getElementById('roll-btn').addEventListener('click',       rollCharacter);
  document.getElementById('start-game-btn').addEventListener('click', startGame);
  document.getElementById('ctx-save-btn').addEventListener('click',   savePromptDefault);
  document.getElementById('ctx-reset-btn').addEventListener('click',  resetPromptDefault);

  // Gender radio
  document.querySelectorAll('input[name="gender"]').forEach(function(el) {
    el.addEventListener('change', onGenderChange);
  });

  // Gameplay toolbar buttons
  document.getElementById('qs-btn').addEventListener('click',        quickSave);
  document.getElementById('sv-save-btn').addEventListener('click',   toggleSaveForm);
  document.getElementById('sv-load-btn').addEventListener('click',   toggleLoadPanel);
  document.getElementById('sv-confirm-btn').addEventListener('click', confirmSave);
  document.getElementById('sv-cancel-btn').addEventListener('click',  hideSaveForm);
  document.getElementById('submit-btn').addEventListener('click',     sendTurn);
  document.getElementById('reset-game-btn').addEventListener('click', resetGame);

  // Load list — event delegation for dynamically rendered save entries
  document.getElementById('load-list').addEventListener('click', function(e) {
    var delBtn  = e.target.closest('[data-delete-id]');
    var infoDiv = e.target.closest('.load-item-info');
    if (delBtn) { deleteGame(delBtn.getAttribute('data-delete-id')); return; }
    if (infoDiv) {
      var item = infoDiv.closest('[data-save-id]');
      if (item) loadGame(item.getAttribute('data-save-id'));
    }
  });

  // Prompt Enter-to-send
  document.getElementById('prompt').addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (_gameStarted) sendTurn(e); }
  });

  // Selection screen save click handlers (event delegation on static containers)
  ['sel-saves-list','sel-autosaves-list'].forEach(function(listId) {
    var el = document.getElementById(listId);
    if (el) {
      el.addEventListener('click', function(e) {
        var item = e.target.closest('[data-save-id]');
        if (item) loadGame(item.getAttribute('data-save-id'));
      });
    }
  });

  // Models, node info, GPU, user identity, selection saves
  loadModels();
  loadNodeInfo();
  loadUserInfo();
  initGpuGauges();
  loadSelectionSaves();

  // Layout
  adjustPadding();
  window.addEventListener('resize', adjustPadding);
})();
