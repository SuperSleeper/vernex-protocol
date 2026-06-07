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

var SLOTS = ['head', 'body', 'hands', 'feet', 'accessory1', 'accessory2', 'weapon'];
var SLOT_NAMES = {
  fantasy: {head:'Helmet',   body:'Cloak/Armor',   hands:'Gauntlets',      feet:'Boots',       accessory1:'Ring',       accessory2:'Amulet',      weapon:'Weapon'},
  scifi:   {head:'Visor',    body:'Exosuit',        hands:'Gloves',         feet:'Boots',       accessory1:'Implant',    accessory2:'Comms Device',weapon:'Weapon'},
  action:  {head:'Hat/Helm', body:'Coat/Uniform',   hands:'Gloves/Holster', feet:'Boots/Spurs', accessory1:'Badge',      accessory2:'Compass',     weapon:'Weapon'},
  comedy:  {head:'Hat/Wig',  body:'Costume/Jacket', hands:'Gloves',         feet:'Shoes',       accessory1:'Name Tag',   accessory2:'Lucky Charm', weapon:'Prop/Gadget'}
};
var SKILL_KEYWORDS = {
  fantasy: [
    {key:'incantation', stat:'Magic',    words:['incantation','cast ','spell','enchant','arcane','conjure','invoke']},
    {key:'battle_cry',  stat:'Strength', words:['battle cry','war cry','charge','strike','smite','cleave']},
    {key:'focus',       stat:'Agility',  words:['dodge','evade','swift','nimble','deft','maneuver']},
    {key:'persuasion',  stat:'Charisma', words:['persuade','charm','deceive','bluff','convince']}
  ],
  scifi: [
    {key:'analysis',   stat:'Intelligence', words:['calculate','analyze','compute','deduce','scan']},
    {key:'diplomacy',  stat:'Charisma',     words:['negotiate','diplomatic','convince','treaty']},
    {key:'tech_use',   stat:'Tech Skill',   words:['trajectory','vector','circuit','override','hack']},
    {key:'logic_trap', stat:'Intelligence', words:['paradox','temporal','causality','loop','timeline']}
  ],
  action: [
    {key:'command',  stat:'Charisma', words:['decree','proclaim','command','order','declare']},
    {key:'oratory',  stat:'Charisma', words:['speech','rally','inspire','address','oration']},
    {key:'bluff',    stat:'Cunning',  words:['bluff','feint','deceive','trick','ruse','gambit']},
    {key:'strategy', stat:'Cunning',  words:['cipher','signal','encrypt','flanking','outmaneuver']}
  ],
  comedy: [
    {key:'excuse',    stat:'Wit',      words:['excuse','explain','justify','apologize']},
    {key:'gossip',    stat:'Charm',    words:['gossip','rumor','heard','whisper']},
    {key:'dignity',   stat:'Charisma', words:['dignified','proper','regal','composed','decorum']},
    {key:'luck_push', stat:'Luck',     words:['fortune','coincidence','twist of fate','stumble upon']}
  ]
};
var RARITY_COLORS = {common:'#8b949e', uncommon:'#3fb950', rare:'#58a6ff', legendary:'#d4a017', cursed:'#f85149'};
var DEFEAT_KEYWORDS = ['defeated','slain','destroyed','falls unconscious','collapses','vanquished','killed','is dead'];
var XP_THRESHOLDS = [3, 6, 10, 15];
var COMBAT_START_WORDS = ['attacks','charges','lunges','draws weapon','raises staff','draws its','enemy appears','ambush','threatens','rushes at','attacks you','leaps at','snarls at','aims at'];
var COMBAT_ENEMY_PATTERNS = {
  fantasy: ['goblin','dark elf','forest wraith','wraith','orc warlord','orc','shadow mage','cursed knight','ancient dragon','dragon','lich king','lich','demon lord','demon'],
  scifi:   ['rogue drone','drone','infected terminal','paradox glitch','glitch','alien warlord','rogue android','android','time anomaly','anomaly','rogue ai','hive mind','paradox entity'],
  action:  ['bandit','rival soldier','street thug','thug','assassin','rival general','corrupt official','warlord','emperor','crime lord'],
  comedy:  ['angry coworker','coworker','town busybody','busybody','bumbling sidekick','sidekick','micromanaging boss','town council','rival superhero','ceo','mayor of chaos','arch-nemesis']
};
var STAMINA_STAT_BY_GENRE = {fantasy:'Stamina', scifi:'Endurance', action:'Stamina', comedy:'Stubbornness'};

// ── State ─────────────────────────────────────────────────────
var _genre = null, _rolledStats = null, _character = null;
var _history = [], _gameStarted = false, _currentSaveId = null, _turnCount = 0;
var _inventory = {equipped: {}, bag: [], bag_capacity: 5};
var _level = 1, _levelXp = 0, _skillUses = {}, _skillRanks = {};
var _pendingLootItem = null;
var _playerHealth = null, _playerMaxHealth = null;
var _inCombat = false, _combatEnemy = null;
var _enemiesDefeated = 0, _bossesDefeated = 0;

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
  _inventory = {equipped: {}, bag: [], bag_capacity: 5};
  _level = 1; _levelXp = 0; _skillUses = {}; _skillRanks = {};
  _pendingLootItem = null;
  _playerMaxHealth = computeMaxHealth();
  _playerHealth = _playerMaxHealth;
  _inCombat = false; _combatEnemy = null;
  _enemiesDefeated = 0; _bossesDefeated = 0;
  removeCombatPanel(); updateHealthBar();
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

// ── Inventory helpers ─────────────────────────────────────────
function computeEffectiveStats() {
  if (!_character) return {};
  var eff = {};
  Object.keys(_character.stats).forEach(function(s) { eff[s] = _character.stats[s]; });
  Object.keys(_inventory.equipped || {}).forEach(function(slot) {
    var item = _inventory.equipped[slot];
    if (!item) return;
    Object.keys(item.stat_effects || {}).forEach(function(s) {
      if (eff.hasOwnProperty(s)) eff[s] = Math.min(20, Math.max(1, eff[s] + item.stat_effects[s]));
    });
    if (item.cursed && item.cursed_revealed) {
      Object.keys(item.curse_effects || {}).forEach(function(s) {
        if (eff.hasOwnProperty(s)) eff[s] = Math.min(20, Math.max(1, eff[s] + item.curse_effects[s]));
      });
    }
  });
  return eff;
}

function rarityClass(item) {
  if (!item) return 'rc-common';
  if (item.cursed && !item.cursed_revealed) return 'rc-uncommon';
  return 'rc-' + (item.rarity || 'common');
}

function formatStatEffects(effects) {
  if (!effects) return '';
  var keys = Object.keys(effects);
  if (!keys.length) return '';
  return keys.map(function(k) {
    var v = effects[k];
    return (v > 0 ? '+' : '') + v + ' ' + k.slice(0, 3);
  }).join(' ');
}

function appendSystemMsg(markdown) {
  var log = document.getElementById('chat-log');
  if (!log) return;
  var div = document.createElement('div');
  div.className = 'msg system';
  div.innerHTML = (typeof marked !== 'undefined')
    ? marked.parse(markdown, {breaks: true, gfm: true})
    : markdown.replace(/\*\*/g, '').replace(/\n/g, '<br>');
  log.appendChild(div);
  requestAnimationFrame(function() { log.scrollTop = log.scrollHeight; });
}

function buildInventoryContext() {
  if (!_character || !_inventory) return '';
  var eff = computeEffectiveStats();
  var equipped = _inventory.equipped || {};
  var bag = _inventory.bag || [];
  var slotNames = SLOT_NAMES[_character.genre] || SLOT_NAMES['fantasy'];
  var hasEquipped = Object.keys(equipped).length > 0;
  if (!hasEquipped && !bag.length && _level === 1) return '';

  var lines = ['\n\n### CURRENT CHARACTER STATE'];
  var xpNeed = XP_THRESHOLDS[Math.min(_level - 1, XP_THRESHOLDS.length - 1)] || 15;
  lines.push('Level: ' + _level + '  XP: ' + _levelXp + '/' + xpNeed);

  lines.push('\nEffective Stats (base + equipment bonuses):');
  Object.keys(_character.stats).forEach(function(s) {
    var base = _character.stats[s], effVal = eff[s], diff = effVal - base;
    lines.push('  ' + s + ': ' + effVal + (diff !== 0 ? ' (base ' + base + (diff > 0 ? ' +' : ' ') + diff + ')' : ''));
  });

  var eqLines = SLOTS.map(function(slot) {
    var item = equipped[slot];
    if (!item) return null;
    var fx = formatStatEffects(item.stat_effects);
    return '  ' + (slotNames[slot] || slot) + ': ' + item.name + (fx ? ' [' + fx + ']' : '');
  }).filter(Boolean);
  if (eqLines.length) { lines.push('\nEquipped:'); eqLines.forEach(function(l) { lines.push(l); }); }

  if (bag.length) {
    lines.push('\nBag (' + bag.length + '/' + (_inventory.bag_capacity || 5) + '):');
    bag.forEach(function(item) {
      var fx = formatStatEffects(item.stat_effects);
      lines.push('  - ' + item.name + (fx ? ' [' + fx + ']' : ''));
    });
  }

  var skills = SKILL_KEYWORDS[_character.genre] || [];
  var ranked = skills.filter(function(sk) { return (_skillRanks[sk.key] || 0) > 0; });
  if (ranked.length) {
    lines.push('\nSkill Ranks:');
    ranked.forEach(function(sk) { lines.push('  ' + sk.key.replace('_', ' ') + ': Rank ' + _skillRanks[sk.key] + ' → ' + sk.stat + ' +' + _skillRanks[sk.key]); });
  }
  if (_playerMaxHealth !== null) {
    lines.push('\nHealth: ' + Math.max(0, _playerHealth || 0) + '/' + _playerMaxHealth);
  }
  if (_inCombat && _combatEnemy) {
    lines.push('IN COMBAT with: ' + _combatEnemy.name
      + ' (HP: ' + _combatEnemy.health + '/' + _combatEnemy.max_health
      + '  DEF: ' + _combatEnemy.defense + '  MGR: ' + _combatEnemy.magic_resist + ')');
  }
  return lines.join('\n');
}

function renderCharSheet() {
  if (!_character) return;
  var genre = _character.genre;
  var slotNames = SLOT_NAMES[genre] || SLOT_NAMES['fantasy'];
  var eff = computeEffectiveStats();
  var equipped = _inventory.equipped || {};
  var bag = _inventory.bag || [];
  var bagCap = _inventory.bag_capacity || 5;
  var skills = SKILL_KEYWORDS[genre] || [];
  var xpNeed = XP_THRESHOLDS[Math.min(_level - 1, XP_THRESHOLDS.length - 1)] || 15;
  var xpPct = Math.min(100, Math.round(_levelXp / xpNeed * 100));

  document.getElementById('cs-name').textContent = _character.name + ' \xb7 ' + _character.subtype + ' \xb7 Lv ' + _level;

  var html = '<div class="cs-stat-grid">';
  Object.keys(_character.stats).forEach(function(n) {
    var base = _character.stats[n], effVal = eff[n], diff = effVal - base;
    var diffStr = diff !== 0
      ? ' <span style="color:' + (diff > 0 ? '#3fb950' : '#f85149') + '">' + (diff > 0 ? '+' : '') + diff + '</span>'
      : '';
    html += '<div class="stat-row">'
      + '<span class="stat-name">' + n + '</span>'
      + '<span class="stat-bar">' + statBar(effVal) + '</span>'
      + '<span class="stat-val">&nbsp;' + effVal + diffStr + '</span>'
      + '</div>';
  });
  html += '</div>';

  // XP bar
  html += '<div class="cs-section"><div class="cs-xp-row">'
    + '<span style="color:#d4a017;font-size:.7rem;font-weight:bold">Lv ' + _level + '</span>'
    + '<div class="cs-xp-track"><div class="cs-xp-fill" style="width:' + xpPct + '%"></div></div>'
    + '<span>' + _levelXp + '/' + xpNeed + ' XP</span>'
    + '</div></div>';

  // Equipped slots
  html += '<div class="cs-section"><div class="cs-section-hdr">Equipped</div>';
  SLOTS.forEach(function(slot) {
    var item = equipped[slot];
    var lbl = escHtml(slotNames[slot] || slot);
    html += '<div class="cs-equipped-slot"><span class="cs-slot-lbl">' + lbl + '</span>';
    if (item) {
      var rc = rarityClass(item);
      var fx = formatStatEffects(item.stat_effects);
      html += '<span class="' + rc + '" style="flex:1;overflow:hidden;white-space:nowrap;text-overflow:ellipsis">' + escHtml(item.name) + '</span>'
        + (fx ? '<span class="cs-item-fx">[' + escHtml(fx) + ']</span>' : '')
        + '<button class="btn-inv" data-unequip="' + escHtml(slot) + '">⬆</button>';
    } else {
      html += '<span style="color:#3d4d5e;font-style:italic;font-size:.65rem;flex:1">— empty —</span>';
    }
    html += '</div>';
  });
  html += '</div>';

  // Bag
  html += '<div class="cs-section">'
    + '<div class="cs-bag-hdr"><div class="cs-section-hdr" style="margin-bottom:0">Bag</div>'
    + '<span class="cs-bag-cap">🎒 ' + bag.length + '/' + bagCap + '</span></div>';
  if (!bag.length) {
    html += '<div style="color:#3d4d5e;font-size:.7rem;font-style:italic;padding:2px 0">Empty</div>';
  } else {
    bag.forEach(function(item) {
      var rc = rarityClass(item);
      var fx = formatStatEffects(item.stat_effects);
      html += '<div class="cs-bag-row">'
        + '<span class="' + rc + '" style="flex:1;overflow:hidden;white-space:nowrap;text-overflow:ellipsis">' + escHtml(item.name) + '</span>'
        + (fx ? '<span class="cs-item-fx">[' + escHtml(fx) + ']</span>' : '')
        + '<button class="btn-inv" data-equip-id="' + escHtml(item.id) + '" data-equip-slot="' + escHtml(item.slot) + '">Equip</button>'
        + '<button class="btn-inv drop" data-drop-id="' + escHtml(item.id) + '" data-drop-name="' + escHtml(item.name) + '">Drop</button>'
        + '</div>';
    });
  }
  html += '</div>';

  // Skills
  if (skills.length) {
    html += '<div class="cs-section"><div class="cs-section-hdr">Skills</div>';
    skills.forEach(function(skill) {
      var rank = _skillRanks[skill.key] || 0;
      var uses = _skillUses[skill.key] || 0;
      var pct = Math.round((uses % 5) / 5 * 100);
      html += '<div class="cs-skill-row">'
        + '<span style="width:80px;overflow:hidden;white-space:nowrap;font-size:.65rem">' + escHtml(skill.key.replace('_', ' ')) + '</span>'
        + '<div class="cs-skill-track"><div class="cs-skill-fill" style="width:' + pct + '%"></div></div>'
        + '<span style="width:28px">' + (uses % 5) + '/5</span>'
        + '<span style="color:' + (rank > 0 ? '#d4a017' : '#3d4d5e') + '">Rk' + rank + '</span>'
        + '</div>';
    });
    html += '</div>';
  }

  document.getElementById('cs-inner').innerHTML = html;

  // Wire inventory buttons (re-attached after innerHTML replace)
  document.querySelectorAll('[data-unequip]').forEach(function(btn) {
    btn.addEventListener('click', function() { doUnequip(btn.getAttribute('data-unequip')); });
  });
  document.querySelectorAll('[data-equip-id]').forEach(function(btn) {
    btn.addEventListener('click', function() { doEquip(btn.getAttribute('data-equip-id'), btn.getAttribute('data-equip-slot')); });
  });
  document.querySelectorAll('[data-drop-id]').forEach(function(btn) {
    btn.addEventListener('click', function() {
      if (confirm('Drop ' + btn.getAttribute('data-drop-name') + '? This cannot be undone.')) {
        doDrop(btn.getAttribute('data-drop-id'));
      }
    });
  });
}

// ── Inventory actions ─────────────────────────────────────────
async function refreshInventoryUI() {
  if (_currentSaveId) {
    try {
      var r = await fetch('/api/game/saves/' + _currentSaveId + '/inventory');
      var d = await r.json();
      if (!d.error) {
        _inventory.equipped = d.equipped || {};
        _inventory.bag = d.bag || [];
        _inventory.bag_capacity = d.bag_capacity || 5;
      }
    } catch(e) {}
  }
  renderCharSheet();
}

async function doEquip(itemId, slot) {
  if (!_currentSaveId) { appendSystemMsg('Save your game first to equip items.'); return; }
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/inventory/equip', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({item_id: itemId, slot: slot})
    });
    var d = await r.json();
    if (d.ok) { await refreshInventoryUI(); }
    else appendSystemMsg('Could not equip: ' + (d.error || 'error'));
  } catch(e) {}
}

async function doUnequip(slot) {
  if (!_currentSaveId) { appendSystemMsg('Save your game first to manage equipment.'); return; }
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/inventory/unequip', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({slot: slot})
    });
    var d = await r.json();
    if (d.ok) { await refreshInventoryUI(); }
    else appendSystemMsg(d.error === 'bag full' ? 'Bag is full — drop an item first.' : 'Could not unequip: ' + (d.error || 'error'));
  } catch(e) {}
}

async function doDrop(itemId) {
  if (!_currentSaveId) { appendSystemMsg('Save your game first to manage items.'); return; }
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/inventory/drop', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({item_id: itemId})
    });
    var d = await r.json();
    if (d.ok) {
      await refreshInventoryUI();
      if (_pendingLootItem) { var it = _pendingLootItem; _pendingLootItem = null; addItemToBag(it); }
    }
  } catch(e) {}
}

async function addItemToBag(item) {
  if (!item) return;
  var fx = formatStatEffects(item.stat_effects);
  if (_currentSaveId) {
    try {
      var r = await fetch('/api/game/saves/' + _currentSaveId + '/inventory/add', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({item: item})
      });
      var d = await r.json();
      if (d.ok) {
        await refreshInventoryUI();
        appendSystemMsg('⚔️ **Loot:** ' + item.name + (fx ? ' [' + fx + ']' : '') + ' added to bag.');
        return;
      } else if (d.bag_full) {
        _pendingLootItem = item;
        appendSystemMsg('🎒 Bag full! Drop an item to pick up **' + item.name + '**' + (fx ? ' [' + fx + ']' : '') + '.');
        return;
      }
    } catch(e) {}
  }
  _inventory.bag.push(item);
  await refreshInventoryUI();
  appendSystemMsg('⚔️ **Loot:** ' + item.name + (fx ? ' [' + fx + ']' : '') + ' added to bag.');
}

async function handleLootDrop(isBoss) {
  if (!_character) return;
  try {
    var r = await fetch('/api/game/item/generate', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({genre: _character.genre, boss: !!isBoss})
    });
    var item = await r.json();
    if (item.error) return;
    await addItemToBag(item);
    handleXpGain(isBoss ? 2 : 1);
  } catch(e) {}
}

function detectLootDrop(text) {
  var lower = text.toLowerCase();
  var defeated = DEFEAT_KEYWORDS.some(function(w) { return lower.indexOf(w) >= 0; });
  if (!defeated) return;
  var bossWords = ['boss','captain','general','king','queen','lord','master','dragon','demon','elder','chief','warlord'];
  var isBoss = bossWords.some(function(w) { return lower.indexOf(w) >= 0; });
  handleLootDrop(isBoss);
}

function handleSkillTracking(text) {
  if (!_character) return;
  var lower = text.toLowerCase();
  var skills = SKILL_KEYWORDS[_character.genre] || [];
  skills.forEach(function(skill) {
    var found = skill.words.some(function(w) { return lower.indexOf(w) >= 0; });
    if (!found) return;
    _skillUses[skill.key] = (_skillUses[skill.key] || 0) + 1;
    var uses = _skillUses[skill.key];
    if (uses % 5 === 0) {
      _skillRanks[skill.key] = (_skillRanks[skill.key] || 0) + 1;
      if (_character.stats.hasOwnProperty(skill.stat)) {
        _character.stats[skill.stat] = Math.min(20, _character.stats[skill.stat] + 1);
      }
      appendSystemMsg('📈 **Skill rank up!** ' + skill.key.replace('_', ' ') + ' → Rank ' + _skillRanks[skill.key] + ' | ' + skill.stat + ' +1');
      renderCharSheet();
    }
  });
}

function handleXpGain(amount) {
  if (!_character) return;
  _levelXp += amount;
  var needed = XP_THRESHOLDS[Math.min(_level - 1, XP_THRESHOLDS.length - 1)] || 15;
  if (_levelXp >= needed && _level < 5) {
    _levelXp -= needed;
    _level++;
    renderCharSheet();
    showLevelUpChoice(_level);
  } else {
    renderCharSheet();
  }
}

function showLevelUpChoice(newLevel) {
  var log = document.getElementById('chat-log');
  if (!log || !_character) return;
  var stats = Object.keys(_character.stats);
  var eff = computeEffectiveStats();
  var div = document.createElement('div');
  div.className = 'msg system';
  div.innerHTML = '<strong style="color:#d4a017">🎉 LEVEL UP! You reached Level ' + newLevel + '!</strong><br>'
    + 'Choose 2 stats to increase by +2:<br>'
    + '<div class="lvl-choices">'
    + stats.map(function(s) {
        return '<label><input type="checkbox" class="lvl-choice" value="' + escHtml(s) + '"> '
          + escHtml(s) + ' (' + (eff[s] || 0) + ')</label>';
      }).join('')
    + '</div><button class="btn-lvl">Confirm</button>';
  div.querySelector('.btn-lvl').addEventListener('click', function() {
    var checked = [].slice.call(div.querySelectorAll('.lvl-choice:checked')).map(function(c) { return c.value; });
    if (checked.length !== 2) { alert('Choose exactly 2 stats.'); return; }
    checked.forEach(function(stat) {
      if (_character.stats.hasOwnProperty(stat)) _character.stats[stat] = Math.min(20, _character.stats[stat] + 2);
    });
    div.innerHTML = '<strong style="color:#d4a017">✅ Level ' + newLevel + ' bonuses applied:</strong> ' + checked.join(' & ') + ' +2 each';
    renderCharSheet();
  });
  log.appendChild(div);
  requestAnimationFrame(function() { log.scrollTop = log.scrollHeight; });
}

// ── Combat helpers ────────────────────────────────────────────
function getStaminaStat() {
  if (!_character) return 10;
  var key = STAMINA_STAT_BY_GENRE[_character.genre] || 'Stamina';
  var eff = computeEffectiveStats();
  return eff[key] || 10;
}

function computeMaxHealth() {
  return Math.max(10, getStaminaStat() * 2);
}

function updateHealthBar() {
  var hbTb = document.getElementById('health-bar-tb');
  var hbFill = document.getElementById('hb-fill');
  var hbLbl = document.getElementById('hb-label');
  if (_playerMaxHealth === null) {
    if (hbTb) hbTb.style.display = 'none';
    return;
  }
  if (hbTb) hbTb.style.display = 'flex';
  var hp = Math.max(0, _playerHealth || 0);
  var pct = Math.round(hp / _playerMaxHealth * 100);
  if (hbFill) {
    hbFill.style.width = pct + '%';
    hbFill.style.background = pct > 50 ? '#3fb950' : pct > 25 ? '#d4a017' : '#e94560';
  }
  if (hbLbl) hbLbl.textContent = hp + '/' + _playerMaxHealth;
}

function renderCombatPanel() {
  var existing = document.getElementById('combat-panel');
  if (!_inCombat || !_combatEnemy) {
    if (existing) existing.remove();
    return;
  }
  var enemy = _combatEnemy;
  var hpPct = Math.max(0, Math.round(enemy.health / enemy.max_health * 100));
  var html = '<div class="combat-name">⚔️ COMBAT — ' + escHtml(enemy.name) + '</div>'
    + '<div class="combat-hp-row">'
    + '<div class="combat-hp-track"><div class="combat-hp-fill" style="width:' + hpPct + '%"></div></div>'
    + '<span class="combat-hp-label">HP: ' + enemy.health + '/' + enemy.max_health
    + '&nbsp;&nbsp;DEF:' + enemy.defense + '&nbsp;MGR:' + enemy.magic_resist + '</span>'
    + '</div>'
    + '<div class="combat-actions">'
    + '<button class="btn-combat" data-cb="physical">⚔️ Physical</button>'
    + '<button class="btn-combat" data-cb="magic">✨ Magic</button>'
    + '<button class="btn-combat" data-cb="skill">🎯 Skill</button>'
    + '<button class="btn-combat btn-flee" data-cb="flee">🏃 Flee</button>'
    + '</div>'
    + '<div id="skill-input-row" style="display:none;align-items:center;gap:6px;margin-top:6px">'
    + '<input type="text" id="skill-text-inp" class="sv-inp" placeholder="Describe your move…" style="flex:1;min-width:0">'
    + '<button class="btn-sv" id="cb-skill-go">Strike!</button>'
    + '</div>';
  var panel;
  if (!existing) {
    panel = document.createElement('div');
    panel.id = 'combat-panel';
    panel.className = 'combat-panel';
    var svForm = document.getElementById('sv-form');
    svForm.parentNode.insertBefore(panel, svForm);
  } else {
    panel = existing;
  }
  panel.innerHTML = html;
  panel.querySelectorAll('[data-cb]').forEach(function(btn) {
    btn.addEventListener('click', function() {
      var action = btn.getAttribute('data-cb');
      if (action === 'physical') doAttack('physical');
      else if (action === 'magic') doAttack('magic');
      else if (action === 'skill') {
        var row = document.getElementById('skill-input-row');
        if (row) row.style.display = row.style.display === 'flex' ? 'none' : 'flex';
      } else if (action === 'flee') doCombatFlee();
    });
  });
  var sg = document.getElementById('cb-skill-go');
  if (sg) sg.addEventListener('click', function() {
    var inp = document.getElementById('skill-text-inp');
    if (inp && inp.value.trim()) doSkillAttack(inp.value.trim());
  });
}

function removeCombatPanel() {
  var p = document.getElementById('combat-panel');
  if (p) p.remove();
}

async function startCombat(enemyName) {
  if (!_currentSaveId) {
    await quickSave();
    if (!_currentSaveId) return;
  }
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/combat/start', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({enemy_name: enemyName || null})
    });
    var d = await r.json();
    if (d.enemy) {
      _combatEnemy = d.enemy;
      _inCombat = true;
      _playerHealth = d.player_health;
      _playerMaxHealth = d.player_max_health;
      updateHealthBar();
      renderCombatPanel();
      appendSystemMsg('⚔️ **Combat started!** ' + d.enemy.name
        + ' — HP: ' + d.enemy.health + '  DEF: ' + d.enemy.defense + '  MGR: ' + d.enemy.magic_resist);
    }
  } catch(e) {}
}

async function doAttack(attackType, qualityOverride) {
  if (!_inCombat || !_currentSaveId) return;
  var quality = qualityOverride || 1;
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/combat/attack', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({attack_type: attackType, skill_quality: quality})
    });
    var d = await r.json();
    handleCombatResult(d, attackType);
  } catch(e) {}
}

async function doSkillAttack(skillText) {
  if (!_inCombat || !_currentSaveId) return;
  var quality = 1;
  try {
    var qr = await fetch('/api/game/combat/skill-quality', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({skill_text: skillText, genre: _character ? _character.genre : 'fantasy'})
    });
    var qd = await qr.json();
    quality = qd.quality || 1;
  } catch(e) {}
  var stars = ['★', '★★', '★★★', '★★★★'][quality - 1] || '★';
  appendSystemMsg('🎯 **Skill:** "' + skillText + '" — Quality ' + stars);
  var row = document.getElementById('skill-input-row');
  if (row) row.style.display = 'none';
  var inp = document.getElementById('skill-text-inp');
  if (inp) inp.value = '';
  await doAttack('skill', quality);
}

async function doCombatFlee() {
  if (!_inCombat || !_currentSaveId) return;
  try {
    var r = await fetch('/api/game/saves/' + _currentSaveId + '/combat/flee', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({})
    });
    var d = await r.json();
    if (d.fled) {
      _inCombat = false; _combatEnemy = null;
      removeCombatPanel();
      appendSystemMsg('🏃 You fled from combat!');
    } else {
      _playerHealth = d.player_health;
      appendSystemMsg('❌ Flee failed! Took ' + d.enemy_damage + ' damage.');
      updateHealthBar();
      if (d.player_dead) { _inCombat = false; removeCombatPanel(); showGameOver(null); }
    }
  } catch(e) {}
}

function handleCombatResult(d, attackType) {
  if (d.error) { appendSystemMsg('⚠️ Combat error: ' + d.error); return; }
  if (_combatEnemy) _combatEnemy.health = d.enemy_health;
  _playerHealth = d.player_health;
  var typeLabel = attackType === 'magic' ? '✨ Magic' : attackType === 'skill' ? '🎯 Skill' : '⚔️ Physical';
  var lines = [typeLabel + ' → **' + d.player_damage + ' damage** to ' + escHtml(d.enemy_name || 'enemy')];
  if (d.enemy_damage > 0) {
    lines.push((d.enemy_name || 'Enemy') + ' counter-attacks → **' + d.enemy_damage + ' damage** to you');
  }
  if (d.special_triggered) {
    var sNames = {fire_breath:'🔥 Fire Breath', mind_control:'🧠 Mind Control',
                  call_reinforcements:'💯 Calls Reinforcements', rumor_spread:'📢 Rumor Spread',
                  power_malfunction:'⚡ Power Malfunction'};
    lines.push('💥 Special: **' + (sNames[d.special_name] || d.special_name) + '**');
  }
  appendSystemMsg(lines.join('\n'));
  updateHealthBar();
  if (d.enemy_defeated) {
    _inCombat = false; _combatEnemy = null;
    _enemiesDefeated++;
    if (d.is_boss) _bossesDefeated++;
    removeCombatPanel();
    appendSystemMsg('🏆 **' + escHtml(d.enemy_name) + ' defeated!** Searching for loot…');
    handleLootDrop(d.is_boss || d.loot_tier === 'legendary');
    handleXpGain(d.is_boss ? 2 : 1);
  } else if (d.player_dead) {
    _inCombat = false; _combatEnemy = null;
    removeCombatPanel();
    showGameOver(d);
  } else {
    renderCombatPanel();
  }
}

function detectCombatStart(text) {
  if (_inCombat) return;
  var lower = text.toLowerCase();
  var signal = COMBAT_START_WORDS.some(function(w) { return lower.indexOf(w) >= 0; });
  if (!signal) return;
  var genre = (_character && _character.genre) || 'fantasy';
  var patterns = COMBAT_ENEMY_PATTERNS[genre] || COMBAT_ENEMY_PATTERNS['fantasy'];
  var name = null;
  for (var i = 0; i < patterns.length; i++) {
    if (lower.indexOf(patterns[i]) >= 0) { name = patterns[i]; break; }
  }
  startCombat(name);
}

function showGameOver(d) {
  var log = document.getElementById('chat-log');
  if (!log) return;
  var existing = document.getElementById('game-over-block');
  if (existing) existing.remove();
  var div = document.createElement('div');
  div.id = 'game-over-block';
  div.className = 'game-over-block';
  var charLine = _character ? escHtml(_character.name) + ' — Level ' + _level + '<br>' : '';
  div.innerHTML = '<div class="go-title">GAME OVER</div>'
    + '<div class="go-stats">'
    + charLine
    + 'Enemies defeated: ' + _enemiesDefeated + '<br>'
    + 'Bosses defeated: ' + _bossesDefeated + '<br>'
    + 'Cause: Combat damage'
    + '</div>'
    + '<div class="go-btns">'
    + '<button class="btn-sv" id="go-load">📂 Load Save</button>'
    + '<button class="btn-sv" id="go-new">🏠 New Game</button>'
    + '</div>';
  log.appendChild(div);
  requestAnimationFrame(function() { log.scrollTop = log.scrollHeight; });
  div.querySelector('#go-load').addEventListener('click', function() { toggleLoadPanel(); div.remove(); });
  div.querySelector('#go-new').addEventListener('click', function() { resetGame(); });
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
    var invCtx = buildInventoryContext();
    var msgs = invCtx
      ? _history.slice(0, -1).concat([{role:'system', content:invCtx}], _history.slice(-1))
      : _history;
    var resp = await gameFetch(msgs, model);
    _history.push({role:'assistant', content:resp});
    appendMsg('assistant', resp);
    _turnCount++;
    if (_turnCount % 5 === 0) autoSave();
    detectCombatStart(resp);
    detectLootDrop(resp);
    handleSkillTracking(resp);
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
  _inventory = {equipped: {}, bag: [], bag_capacity: 5};
  _level = 1; _levelXp = 0; _skillUses = {}; _skillRanks = {};
  _pendingLootItem = null;
  _playerHealth = null; _playerMaxHealth = null;
  _inCombat = false; _combatEnemy = null;
  _enemiesDefeated = 0; _bossesDefeated = 0;
  removeCombatPanel(); updateHealthBar();
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
    stats: _character.stats, history: _history,
    equipped: _inventory.equipped, bag: _inventory.bag, bag_capacity: _inventory.bag_capacity,
    level: _level, level_xp: _levelXp, skill_uses: _skillUses, skill_ranks: _skillRanks,
    current_health: _playerHealth, max_health: _playerMaxHealth,
    in_combat: _inCombat, enemy_state: _combatEnemy,
    enemies_defeated: _enemiesDefeated, bosses_defeated: _bossesDefeated
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
    stats: _character.stats, history: _history, autosave: true,
    equipped: _inventory.equipped, bag: _inventory.bag, bag_capacity: _inventory.bag_capacity,
    level: _level, level_xp: _levelXp, skill_uses: _skillUses, skill_ranks: _skillRanks,
    current_health: _playerHealth, max_health: _playerMaxHealth,
    in_combat: _inCombat, enemy_state: _combatEnemy,
    enemies_defeated: _enemiesDefeated, bosses_defeated: _bossesDefeated
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
    _inventory = {equipped: s.equipped || {}, bag: s.bag || [], bag_capacity: s.bag_capacity || 5};
    _level = s.level || 1; _levelXp = s.level_xp || 0;
    _skillUses = s.skill_uses || {}; _skillRanks = s.skill_ranks || {};
    _pendingLootItem = null;
    _playerMaxHealth = s.max_health || computeMaxHealth();
    _playerHealth = s.current_health !== undefined ? s.current_health : _playerMaxHealth;
    _inCombat = !!(s.in_combat && s.enemy_state && !s.enemy_state.defeated);
    _combatEnemy = _inCombat ? s.enemy_state : null;
    _enemiesDefeated = s.enemies_defeated || 0;
    _bossesDefeated = s.bosses_defeated || 0;
    removeCombatPanel(); updateHealthBar();
    renderCharSheet();
    showView('view-play');
    hideLoadPanel();
    var log = document.getElementById('chat-log');
    log.innerHTML = '';
    var last = _history.filter(function(m) { return m.role === 'assistant'; });
    if (last.length) appendMsg('assistant', last[last.length - 1].content);
    if (_inCombat) renderCombatPanel();
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
