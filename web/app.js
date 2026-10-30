'use strict';
(() => {
  const $ = (selector) => document.querySelector(selector);
  const grid = $('#sound-grid');
  const status = $('#queue-status');
  const playingLabel = $('#playing-label');
  const playingBy = $('#playing-by');
  const queueDot = $('#queue-dot');
  const instantName = $('#instant-name');
  const instantBy = $('#instant-by');
  const instantDot = $('#instant-dot');
  const waitingLabel = $('#waiting-label');
  const toast = $('#toast');
  const feedback = $('#feedback-banner');
  const onlineCount = $('#online-count');
  const presenceList = $('#presence-list');
  const nicknameDialog = $('#nickname-dialog');
  const nicknameForm = $('#nickname-form');
  const nicknameInput = $('#nickname-input');
  const nicknameError = $('#nickname-error');
  const cancelName = $('#nickname-cancel');
  const myNickname = $('#my-nickname');
  const audioActivate = $('#audio-activate');
  const volume = $('#volume');
  const volumeValue = $('#volume-value');
  const player = SoundboardAudio.create();
  const instantPlayer = SoundboardAudio.create();
  const instantRow = $('#instant-row');
  const queueMain = $('#queue-main');
  const adminBanner = $('#admin-mode-banner');
  const filterToggle = $('#filter-toggle');
  const filterPanel = $('#filter-panel');
  const filterSearch = $('#filter-search');
  const filterGroup = $('#filter-group');
  const filterSort = $('#filter-sort');
  const filterReset = $('#filter-reset');
  const filterSummary = $('#filter-summary');
  const filterActiveDot = $('#filter-active-dot');

  let nick = '';
  let participantID = '';
  const pulseTimers = new Map();
  let socket = null;
  let reconnectTimer = null;
  let reconnectDelay = 750;
  let toastTimer = null;
  let requestID = 0;
  let sounds = [];
  let groups = [];
  let queue = { playing: null, waiting: 0, limit: 20 };
  let online = [];
  let instant = { sequence: 0, playing: null };
  let adminMode = false;
  let playGeneration = 0;
  let instantGeneration = 0;
  let audioUnlocked = false;
  let connected = false;
  const pending = new Map();
  const buttons = new Map();

  const collator = new Intl.Collator('de', { numeric: true, sensitivity: 'base' });
  let searchTerm = '';
  let selectedGroup = '';
  let sortMode = 'asc';

  function storageGet(key, fallback) {
    try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
  }
  function storageSet(key, val) { try { localStorage.setItem(key, val); } catch { /* private mode */ } }
  function notify(message) {
    toast.textContent = message;
    toast.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove('show'), 2200);
  }
  function showBanner(message) { feedback.textContent = message; feedback.hidden = !message; }
  function colorForGroup(group) {
    return groups.find(g => g.name === group)?.color || '#8492a9';
  }
  function canSend() { return connected && !!nick; }
  function isAdminActive() { return online.some(u => u.id === participantID && u.admin); }
  function updateAdminMode() {
    const next = isAdminActive();
    if (next === adminMode) return;
    adminMode = next;
    adminBanner.hidden = !next;
    if (next) { stopAudio(); stopInstantAudio(); }
  }
  function updateButtons() {
    const can = canSend();
    for (const [filename, button] of buttons) {
      button.disabled = !can;
      const isPlaying = queue.playing?.filename === filename;
      const isInstant = instant.playing?.filename === filename;
      button.classList.toggle('is-playing', isPlaying);
      button.classList.toggle('is-instant', isInstant);
      if (isInstant) button.style.setProperty('--instant-color', instant.playing.byColor || '#e6a34a');
      else button.style.removeProperty('--instant-color');
      if (isPlaying) button.style.setProperty('--player-color', queue.playing.byColor || '#41ad7f');
      else button.style.removeProperty('--player-color');
      button.title = !connected ? 'Nicht verbunden' : !nick ? 'Bitte zuerst Nickname wählen' : queue.waiting >= queue.limit ? `${button.dataset.title} · Warteschlange voll, Sofortwiedergabe weiterhin möglich` : button.dataset.title;
    }
  }
  function renderState() {
    const current = queue.playing;
    const activeQueue = !!current || queue.waiting > 0;
    const activeInstant = !!instant.playing;
    const isVisible = activeQueue || activeInstant;
    status.hidden = !isVisible;
    document.body.classList.toggle('has-playback-status', isVisible);
    // Keep channel order fixed, but omit inactive rows. The panel is anchored
    // to the bottom and therefore grows upward when both channels are active.
    instantRow.hidden = !activeInstant;
    queueMain.hidden = !activeQueue;
    instantName.textContent = instant.playing?.name || 'Kein Sofort-Sound';
    instantBy.textContent = instant.playing?.by || '';
    instantDot.style.background = instant.playing?.byColor || 'transparent';
    instantDot.hidden = !activeInstant;
    playingLabel.textContent = current?.name || (queue.waiting ? 'Wartet auf nächsten Sound' : 'Keine Queue-Wiedergabe');
    playingBy.textContent = current?.by || '';
    queueDot.style.background = current?.byColor || 'transparent';
    queueDot.hidden = !current;
    waitingLabel.textContent = queue.waiting ? `${queue.waiting} warten` : '';
    // Keep transient feedback above the actual panel height (one or two rows).
    document.body.style.setProperty('--playback-panel-height', isVisible ? `${status.offsetHeight}px` : '0px');
    updateButtons();
    renderPresence();
  }
  function normalizeQuery(value) {
    return value.toLocaleLowerCase('de').normalize('NFD').replace(/[\u0300-\u036f]/g, '');
  }
  function currentFiltersActive() { return !!searchTerm.trim() || !!selectedGroup || sortMode !== 'asc'; }
  function updateFilterGroups() {
    const present = new Set(sounds.map(sound => sound.group || 'default'));
    for (const group of groups) if (group.name) present.add(group.name);
    filterGroup.replaceChildren(new Option('Alle Gruppen', ''));
    for (const name of [...present].sort(collator.compare)) filterGroup.add(new Option(name, name));
    if (selectedGroup && !present.has(selectedGroup)) {
      selectedGroup = '';
      storageSet('soundboard.filter.group', '');
    }
    filterGroup.value = selectedGroup;
  }
  function renderSounds() {
    grid.replaceChildren();
    buttons.clear();
    updateFilterGroups();
    const query = normalizeQuery(searchTerm.trim());
    const ordered = sounds.filter(s =>
      (!selectedGroup || (s.group || 'default') === selectedGroup) &&
      (!query || normalizeQuery(s.name).includes(query))
    ).sort((a, b) => {
      if (sortMode === 'group') {
        const groupOrder = collator.compare(a.group || 'default', b.group || 'default');
        if (groupOrder) return groupOrder;
      }
      const nameOrder = collator.compare(a.name, b.name) || collator.compare(a.filename, b.filename);
      return sortMode === 'desc' ? -nameOrder : nameOrder;
    });
    filterSummary.textContent = `${ordered.length} / ${sounds.length} Sounds`;
    filterToggle.classList.toggle('has-filters', currentFiltersActive());
    filterActiveDot.hidden = !currentFiltersActive();
    const empty = $('#empty-state');
    empty.hidden = ordered.length > 0;
    empty.textContent = sounds.length ? 'Keine passenden Sounds gefunden.' : 'Noch keine Sounds vorhanden.';
    for (const sound of ordered) {
      const item = document.createElement('div');
      item.className = 'sound-item';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'sound-trigger';
      const tone = colorForGroup(sound.group || 'default');
      button.style.setProperty('--tone', tone);
      button.style.setProperty('--edge', `color-mix(in srgb, ${tone} 68%, black)`);
      button.style.setProperty('--edge-dark', `color-mix(in srgb, ${tone} 53%, black)`);
      button.dataset.title = `${sound.name} · Gruppe: ${sound.group || 'default'} · Klick: sofort · Shift + Klick: einreihen`;
      button.title = button.dataset.title;
      button.setAttribute('aria-label', `${sound.name}, Gruppe ${sound.group || 'default'}, Klick: sofort bei allen abspielen; Shift-Klick oder langer Touch: Warteschlange`);
      const cap = document.createElement('span');
      cap.className = 'sound-cap';
      cap.setAttribute('aria-hidden', 'true');
      const label = document.createElement('span');
      label.className = 'sound-name';
      label.textContent = sound.name;
      label.title = button.dataset.title;
      button.append(cap);
      item.append(button, label);
      grid.append(item);
      buttons.set(sound.filename, button);
      let holdTimer = null;
      let suppressClick = false;
      let startX = 0, startY = 0;
      const clearHold = () => { clearTimeout(holdTimer); holdTimer = null; };
      button.addEventListener('pointerdown', e => {
        if (e.pointerType !== 'touch' && e.pointerType !== 'pen') return;
        clearHold(); startX = e.clientX; startY = e.clientY;
        holdTimer = setTimeout(() => {
          holdTimer = null;
          suppressClick = true;
          enqueue(sound);
          if (navigator.vibrate) navigator.vibrate(25);
          setTimeout(() => { suppressClick = false; }, 1000);
        }, 550);
      });
      button.addEventListener('pointermove', e => {
        if (Math.hypot(e.clientX - startX, e.clientY - startY) > 12) clearHold();
      });
      button.addEventListener('pointerup', clearHold);
      button.addEventListener('pointercancel', clearHold);
      button.addEventListener('contextmenu', e => { if (e.pointerType === 'touch' || suppressClick) e.preventDefault(); });
      button.addEventListener('click', e => {
        if (suppressClick) { e.preventDefault(); suppressClick = false; return; }
        if (e.shiftKey) enqueue(sound);
        else triggerInstant(sound);
      });
    }
    updateButtons();
  }
  function pulseSound(filename, color) {
    const button = buttons.get(filename);
    if (!button) return;
    button.style.setProperty('--pulse-color', color || '#41ad7f');
    button.classList.remove('is-queued');
    void button.offsetWidth; // restart the one-shot animation for successive clicks
    button.classList.add('is-queued');
    clearTimeout(pulseTimers.get(filename));
    pulseTimers.set(filename, setTimeout(() => button.classList.remove('is-queued'), 950));
  }
  function renderPresence() {
    onlineCount.textContent = String(online.length); // distinct browser identities, not open tabs
    presenceList.replaceChildren();
    if (!online.length) {
      const li = document.createElement('li');
      li.className = 'no-users';li.textContent='Niemand online';presenceList.append(li);
    }
    for (const person of online) {
      const li=document.createElement('li');li.className='participant';
      const dot=document.createElement('span');dot.className='participant-dot';
      dot.style.background=person.color||'#8291a4';
      const label=document.createElement('span');label.className='participant-name';label.textContent=person.name;
      li.append(dot,label);
      li.dataset.participantId=person.id;
      if (person.id===participantID) {const me=document.createElement('small');me.textContent='Du';me.className='participant-me';li.append(me)}
      if (person.connections>1) {const n=document.createElement('small');n.textContent=`×${person.connections}`;li.append(n)}
      if (person.admin) {
        const mark=document.createElement('span');mark.className='participant-admin';mark.textContent='⚙ Admin';mark.title='In der Administration · Soundboard stumm';li.append(mark);
      }
      if (queue.playing?.byId===person.id) {const active=document.createElement('span');active.className='participant-playing';active.textContent='♫';active.title='Spielt gerade';active.setAttribute('aria-label','Spielt gerade');li.append(active)}
      if (instant.playing?.byId===person.id) {const active=document.createElement('span');active.className='participant-instant';active.textContent='⚡';active.title='Spielt einen Sofort-Sound';active.setAttribute('aria-label','Spielt einen Sofort-Sound');li.append(active)}
      const count=queue.waitingById?.[person.id]||0;
      if (count>0) {const badge=document.createElement('span');badge.className='participant-waiting';badge.textContent=String(count);badge.title=`${count} wartende Sounds`;badge.setAttribute('aria-label',`${count} wartende Sounds`);li.append(badge)}
      if (person.id===participantID) li.classList.add('is-me');
      if (queue.playing?.byId===person.id || instant.playing?.byId===person.id) li.classList.add('is-active');
      if (person.admin) li.classList.add('is-admin');
      presenceList.append(li);
    }
  }
  function highlightUser(id) {
    if (!id) return;
    for (const row of presenceList.querySelectorAll('.participant')) {
      if (row.dataset.participantId !== id) continue;
      row.classList.remove('is-triggering'); void row.offsetWidth;
      row.classList.add('is-triggering');
      setTimeout(() => row.classList.remove('is-triggering'), 950);
    }
  }
  function applyName(name) {
    nick = name;
    myNickname.textContent = name;
    storageSet('soundboard.nickname', name);
    if (connected) send({ type: 'set_name', name, participantId: participantID });
    updateButtons();
    renderPresence();
  }
  function send(message) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify(message));
    return true;
  }
  function enqueue(sound) {
    if (!canSend()) return;
    if (queue.waiting >= queue.limit) { notify('Warteschlange voll – normaler Klick spielt sofort'); return; }
    const id = ++requestID;
    pending.set(id, sound.name);
    if (!send({ type: 'enqueue', filename: sound.filename, requestId: id })) {
      pending.delete(id);
      notify('Keine Verbindung');
    }
  }
  function triggerInstant(sound) {
    if (!canSend()) return;
    const id = ++requestID;
    pending.set(id, sound.name);
    if (!send({type:'instant',filename:sound.filename,requestId:id})) {
      pending.delete(id); notify('Keine Verbindung');
    }
  }
  function stopAudio() { playGeneration++; SoundboardAudio.stop(player); }
  function stopInstantAudio() { instantGeneration++; SoundboardAudio.stop(instantPlayer); }
  function playChannel(audio, playing, immediate=false) {
    if (immediate) stopInstantAudio(); else stopAudio();
    if (!playing || adminMode) return;
    const gen = immediate ? instantGeneration : playGeneration;
    audio.volume = Number(volume.value) / 100;
    SoundboardAudio.play(audio, playing).then(() => {
      if ((immediate ? instantGeneration : playGeneration) !== gen || adminMode) return;
      audioUnlocked = true;
      audioActivate.hidden = true;
    }).catch(error => {
      if ((immediate ? instantGeneration : playGeneration) !== gen || adminMode || error?.name === 'AbortError') return;
      if (error?.name === 'NotAllowedError') {
        audioUnlocked = false;
        audioActivate.hidden = false;
        notify('Audio aktivieren, um Sounds zu hören');
      } else {
        notify(`Audio konnte nicht abgespielt werden: ${playing.name}`);
      }
    });
  }
  function playSound(playing) { playChannel(player, playing); }
  function playInstantSound(playing) { playChannel(instantPlayer, playing, true); }
  function applyInstant(state, start=false) {
    if (!state || state.sequence <= instant.sequence) return;
    instant = state;
    if (start) playInstantSound(state.playing);
    else stopInstantAudio();
    renderState();
  }
  function connect() {
    clearTimeout(reconnectTimer);
    if (socket && socket.readyState <= WebSocket.OPEN) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    socket = ws;
    $('#connection').textContent = 'Verbinde …';
    $('#connection').classList.remove('error');
    ws.addEventListener('open', () => {
      if (ws !== socket) return;
      connected = true;
      reconnectDelay = 750;
      $('#connection').textContent = 'Verbunden';
      $('#connection').classList.remove('error');
      showBanner('');
      if (nick) send({ type: 'set_name', name: nick, participantId: participantID });
      updateButtons();
    });
    ws.addEventListener('message', (event) => {
      let msg;
      try { msg = JSON.parse(event.data); } catch { return; }
      if (msg.type === 'init') {
        // Treat reconnect as a fresh sync; do not replay the current instant sound.
        sounds = msg.sounds || [];
        groups = msg.groups || [];
        queue = msg.state || queue;
        instant = msg.instant || {sequence:0,playing:null};
        online = msg.users || [];
        updateAdminMode();
        renderSounds(); renderState(); renderPresence();
      } else if (msg.type === 'library') {
        sounds = msg.sounds || [];
        groups = msg.groups || [];
        renderSounds(); renderState();
      } else if (msg.type === 'state') {
        queue = msg.state;
        renderState();
      } else if (msg.type === 'play') {
        playSound(msg.playing);
      } else if (msg.type === 'instant') {
        applyInstant(msg.state, !!msg.start);
      } else if (msg.type === 'presence') {
        online = msg.users || [];
        updateAdminMode();
        renderPresence();
      } else if (msg.type === 'queued') {
        pulseSound(msg.filename, msg.byColor);
        highlightUser(msg.byId);
      } else if (msg.type === 'instant_ack') {
        const name=pending.get(msg.requestId);
        pending.delete(msg.requestId);
        if (!msg.ok) notify(msg.error || 'Sofortwiedergabe fehlgeschlagen');
      } else if (msg.type === 'ack') {
        const name = pending.get(msg.requestId);
        pending.delete(msg.requestId);
        if (msg.ok && msg.position > 0) notify(`${name} eingereiht · Position ${msg.position}`);
        else notify(msg.error === 'queue full' ? 'Warteschlange voll' : (msg.error || 'Einreihen fehlgeschlagen'));
      } else if (msg.type === 'name_error') {
        showBanner(msg.error || 'Nickname ungültig');
      }
    });
    ws.addEventListener('close', () => {
      if (ws !== socket) return;
      connected = false;
      online = [];
      queue = { playing: null, waiting: 0, limit: 20 };
      instant = {sequence:0,playing:null};
      stopAudio(); stopInstantAudio();
      renderPresence(); renderState();
      $('#connection').textContent = 'Verbindung unterbrochen';
      $('#connection').classList.add('error');
      showBanner('Die Verbindung wurde unterbrochen. Verbindung wird automatisch wiederhergestellt.');
      pending.clear();
      reconnectTimer = setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(10000, reconnectDelay * 1.7);
    });
    ws.addEventListener('error', () => ws.close());
  }
  function openNameDialog(edit = false) {
    nicknameError.hidden = true;
    nicknameInput.value = nick;
    cancelName.hidden = !edit || !nick;
    if (!nicknameDialog.open) nicknameDialog.showModal();
    nicknameInput.focus();
    nicknameInput.select();
  }
  function unlockAudio() {
    // A tiny silent WAV played within a user gesture primes the same audio
    // element that receives actual sounds later. No sound files are preloaded.
    const sampleRate = 8000, samples = 400;
    const data = new Uint8Array(44 + samples * 2);
    const view = new DataView(data.buffer);
    const str = (at, text) => { for (let i = 0; i < text.length; i++) view.setUint8(at + i, text.charCodeAt(i)); };
    str(0, 'RIFF'); view.setUint32(4, data.length - 8, true); str(8, 'WAVE'); str(12, 'fmt ');
    view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true);
    view.setUint32(24, sampleRate, true); view.setUint32(28, sampleRate * 2, true);
    view.setUint16(32, 2, true); view.setUint16(34, 16, true); str(36, 'data');
    view.setUint32(40, samples * 2, true);
    const base64 = btoa(Array.from(data, b => String.fromCharCode(b)).join(''));
    const sample = `data:audio/wav;base64,${base64}`;
    for (const audio of [player, instantPlayer]) {
      audio.src = sample;
      audio.volume = Number(volume.value) / 100;
    }
    // Unlock both independently: a browser may gate each media element.
    Promise.all([player.play(), instantPlayer.play()]).then(() => {
      audioUnlocked = true;
      audioActivate.hidden = true;
    }).catch(() => {
      audioUnlocked = false;
      audioActivate.hidden = false;
    });
  }

  // Filtering and sorting only affect this browser's visible button list.
  // Global playback and notifications continue even for hidden sounds.
  searchTerm = storageGet('soundboard.filter.search', '');
  selectedGroup = storageGet('soundboard.filter.group', '');
  sortMode = storageGet('soundboard.filter.sort', 'asc');
  if (!['asc', 'desc', 'group'].includes(sortMode)) sortMode = 'asc';
  filterSearch.value = searchTerm;
  filterSort.value = sortMode;
  function setFiltersExpanded(expanded, focus = false) {
    filterPanel.hidden = !expanded;
    filterToggle.setAttribute('aria-expanded', String(expanded));
    storageSet('soundboard.filter.expanded', expanded ? 'true' : 'false');
    if (expanded && focus) filterSearch.focus();
  }
  setFiltersExpanded(storageGet('soundboard.filter.expanded', 'false') === 'true');
  filterToggle.addEventListener('click', () => setFiltersExpanded(filterPanel.hidden, true));
  filterSearch.addEventListener('input', () => {
    searchTerm = filterSearch.value;
    storageSet('soundboard.filter.search', searchTerm);
    renderSounds();
  });
  filterGroup.addEventListener('change', () => {
    selectedGroup = filterGroup.value;
    storageSet('soundboard.filter.group', selectedGroup);
    renderSounds();
  });
  filterSort.addEventListener('change', () => {
    sortMode = filterSort.value;
    storageSet('soundboard.filter.sort', sortMode);
    renderSounds();
  });
  filterReset.addEventListener('click', () => {
    searchTerm = ''; selectedGroup = ''; sortMode = 'asc';
    filterSearch.value = ''; filterGroup.value = ''; filterSort.value = 'asc';
    storageSet('soundboard.filter.search', '');
    storageSet('soundboard.filter.group', '');
    storageSet('soundboard.filter.sort', 'asc');
    renderSounds();
    filterSearch.focus();
  });
  filterSearch.addEventListener('keydown', event => {
    if (event.key === 'Escape') { filterSearch.value = ''; searchTerm = ''; storageSet('soundboard.filter.search', ''); renderSounds(); }
  });

  nicknameForm.addEventListener('submit', (event) => {
    event.preventDefault();
    const name = nicknameInput.value.trim();
    if (!name || [...name].length > 32 || /[\u0000-\u001f\u007f]/u.test(name)) { nicknameError.hidden = false; return; }
    unlockAudio(); // must be synchronous with the user's click
    applyName(name);
    nicknameDialog.close();
  });
  cancelName.addEventListener('click', () => nicknameDialog.close());
  nicknameDialog.addEventListener('cancel', (event) => { if (!nick) event.preventDefault(); });
  $('#name-button').addEventListener('click', () => openNameDialog(true));
  audioActivate.addEventListener('click', () => unlockAudio());
  volume.value = storageGet('soundboard.volume', '70');
  volumeValue.textContent = `${volume.value}%`;
  player.volume = Number(volume.value) / 100;
  instantPlayer.volume = player.volume;
  volume.addEventListener('input', () => {
    volumeValue.textContent = `${volume.value}%`;
    player.volume = Number(volume.value) / 100;
    instantPlayer.volume = Number(volume.value) / 100;
    storageSet('soundboard.volume', volume.value);
  });
  window.addEventListener('online', () => { if (!connected) connect(); });
  window.addEventListener('storage', (event) => {
    if (event.key === 'soundboard.nickname' && event.newValue && event.newValue !== nick) applyName(event.newValue);
  });
  // play() rejection handlers above already report real audio errors.

  participantID = storageGet('soundboard.participantId', '');
  if (!/^[a-zA-Z0-9-]{8,80}$/.test(participantID)) {
    participantID = typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : `local-${Date.now()}-${Math.floor(Math.random()*1e9)}`;
    storageSet('soundboard.participantId', participantID);
  }
  nick = storageGet('soundboard.nickname', '').trim();
  if (nick && [...nick].length <= 32) myNickname.textContent = nick;
  else { nick = ''; openNameDialog(); }
  audioActivate.hidden = !!nick; // returning visitors may need to activate browser audio
  connect();
})();
