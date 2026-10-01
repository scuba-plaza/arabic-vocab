'use strict';

const POS = ['noun', 'verb', 'adj', 'adv', 'prep', 'conj', 'particle', 'pron', 'num', 'intj', 'phrase', 'det'];
const POS_LABEL = {
  adj: 'adjective', adv: 'adverb', prep: 'preposition', conj: 'conjunction', pron: 'pronoun',
  num: 'number', intj: 'interjection', det: 'determiner',
};
const GENDER_LABEL = { m: 'masc.', f: 'fem.', 'm+f': 'masc./fem.' };
const KEY_LABEL = { enter: 'enter', esc: 'esc', left: '←', right: '→', 'ctrl+enter': 'ctrl+enter' };
const FIX_LABEL = { catt: 'Use CATT\'s ending', camel: 'Use CAMeL\'s ending', both: 'Add the ending' };
const PLAY_KEY = { word: 'w', forms: 'f', sentence: 's' };

const state = {
  view: null,
  current: 0,
  shown: null,
  mode: 'review',
  draft: null,
  parts: null,
  original: '',
  message: { text: '', tone: 'info' },
  playing: '',
  voices: null,
  voicesNote: '',
  poll: 0,
  seq: 0,
  pending: 0,
  finished: false,
  ended: false,
  summary: null,
};

const player = new Audio();

function $(id) {
  return document.getElementById(id);
}

function el(tag, cls, ...kids) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  for (const kid of kids) {
    if (kid !== null && kid !== undefined && kid !== false) node.append(kid);
  }
  return node;
}

function spans(list) {
  const frag = document.createDocumentFragment();
  for (const s of list || []) {
    frag.append(s.c ? el('span', s.c, s.t) : s.t);
  }
  return frag;
}

function arabic(cls, list) {
  const node = el('span', 'ar' + (cls ? ' ' + cls : ''), spans(list));
  node.dir = 'rtl';
  return node;
}

function plural(n, one, many) {
  return n + ' ' + (n === 1 ? one : many);
}

async function request(path, options) {
  const res = await fetch(path, options);
  let body = null;
  try {
    body = await res.json();
  } catch (err) {
    body = null;
  }
  if (!res.ok) {
    const err = new Error(body && body.message ? body.message : res.status + ' ' + res.statusText);
    err.status = res.status;
    throw err;
  }
  return body;
}

function shown() {
  return state.view && state.view.entry ? state.view.entry.index : -1;
}

function post(action, body) {
  return request('api/' + action + '?i=' + shown(), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function failed(err) {
  if (err.status) {
    say(err.message, 'bad');
    return;
  }
  state.finished = true;
  state.ended = true;
  clearTimeout(state.poll);
  stopAudio();
  render();
}

async function load(i, quiet) {
  state.seq += 1;
  state.pending += 1;
  const seq = state.seq;
  let view;
  try {
    view = await request('api/state?i=' + i);
  } catch (err) {
    failed(err);
    return;
  } finally {
    state.pending -= 1;
  }
  if (seq !== state.seq) return;
  announce(view);
  state.view = view;
  state.current = view.entry ? view.entry.index : -1;
  if (quiet && state.mode === 'edit') {
    renderChrome();
  } else {
    render();
  }
  schedule();
}

async function act(action, body) {
  let res;
  try {
    res = await post(action, body);
  } catch (err) {
    failed(err);
    return;
  }
  if (res.message) say(res.message, res.tone || 'info');
  if (res.show !== state.current) stopAudio();
  state.current = res.show;
  await load(res.show);
  return res;
}

async function remake(kind) {
  say('♪ Making the ' + kind + ' audio with Google Text-to-Speech…', 'audio');
  if (!(await act('remake', { clip: kind }))) return;
  const e = state.view && state.view.entry;
  if (e && ready(e, kind)) play(kind);
}

function removeClip(kind) {
  if (!window.confirm('Delete the ' + kind + ' MP3? Remake or \'arabic-vocab audio\' makes it again.')) return;
  act('remove', { clip: kind });
}

function announce(view) {
  const before = state.view ? state.view.list : [];
  const here = view.entry ? view.entry.index : -1;
  for (const item of view.list) {
    const old = before[item.index];
    if (!old || !old.asking || item.asking) continue;
    const where = item.index === here ? '' : ' (note ' + (item.index + 1) + ')';
    if (item.ready) {
      say('✦ Claude Code\'s version of ' + item.arabic + ' is ready' + where, 'claude');
    } else if (item.notice) {
      say(item.notice + where, item.notice_tone === 'bad' ? 'bad' : 'info');
    }
  }
}

function schedule() {
  clearTimeout(state.poll);
  const v = state.view;
  if (!v || state.finished || !v.list.some((x) => x.asking)) return;
  state.poll = setTimeout(() => load(state.current, true), 1000);
}

function say(text, tone) {
  state.message = { text, tone: tone || 'info' };
  renderStatus();
}

function snapshot() {
  return JSON.stringify([state.draft, state.parts]);
}

function dirty() {
  return state.mode === 'edit' && snapshot() !== state.original;
}

function leaveEdit() {
  return !dirty() || window.confirm('Discard your changes to this note?');
}

window.addEventListener('beforeunload', (ev) => {
  if (dirty()) ev.preventDefault();
});

function go(i) {
  if (!leaveEdit()) return;
  if (state.mode === 'edit') {
    state.mode = 'review';
    state.draft = null;
  }
  if (i !== state.current) stopAudio();
  state.current = i;
  load(i);
}

function move(d) {
  const total = state.view.list.length;
  if (state.current < 0) {
    if (d < 0 && total) go(total - 1);
    return;
  }
  const next = state.current + d;
  if (next < 0) return;
  go(next >= total ? -1 : next);
}

function clipOf(e, kind) {
  return (e.clips || []).find((c) => c.kind === kind) || null;
}

function ready(e, kind) {
  const c = clipOf(e, kind);
  return !!(c && c.ready);
}

function play(kind) {
  const e = state.view && state.view.entry;
  if (!e) return;
  if (!ready(e, kind)) {
    say('There is no audio for this ' + kind + ' yet; \'Remake\' or \'arabic-vocab audio\' makes it', 'info');
    return;
  }
  player.pause();
  player.src = 'audio?i=' + shown() + '&clip=' + kind + '&v=' + Date.now();
  state.playing = kind;
  renderStatus();
  player.play().catch((err) => {
    state.playing = '';
    say('Could not play the ' + kind + ': ' + err.message, 'bad');
  });
}

function stopAudio() {
  const was = state.playing;
  state.playing = '';
  player.pause();
  if (was) renderStatus();
}

player.addEventListener('ended', () => {
  state.playing = '';
  renderStatus();
});

player.addEventListener('error', () => {
  if (!state.playing) return;
  state.playing = '';
  say('Could not play the clip', 'bad');
});

async function finish() {
  if (!leaveEdit()) return;
  let sum;
  try {
    sum = await request('api/finish', { method: 'POST' });
  } catch (err) {
    failed(err);
    return;
  }
  state.finished = true;
  state.summary = sum;
  clearTimeout(state.poll);
  stopAudio();
  render();
}

function splitExample(markup) {
  const m = /^([^<]*)<b>([^<]*)<\/b>([^<]*)$/i.exec(markup || '');
  return m ? { sentence: m[1] + m[2] + m[3], word: m[2], at: m[1].length } : null;
}

function composeExample() {
  const p = state.parts;
  if (!p) return state.draft.example || '';
  const word = p.word.trim();
  if (!word) return p.sentence;
  let at = p.sentence.substr(p.at, word.length) === word ? p.at : p.sentence.indexOf(word);
  if (at < 0) return null;
  return p.sentence.slice(0, at) + '<b>' + word + '</b>' + p.sentence.slice(at + word.length);
}

function startEdit(fromProposal) {
  const e = state.view.entry;
  state.draft = structuredClone(fromProposal ? e.proposal : e.note);
  state.draft.forms = state.draft.forms || [];
  state.parts = splitExample(state.draft.example);
  state.original = snapshot();
  state.mode = 'edit';
  stopAudio();
  render();
  const first = $('stage').querySelector('.editor input, .editor textarea');
  if (first) first.focus();
}

function cancelEdit() {
  state.mode = 'review';
  state.draft = null;
  state.parts = null;
  say('Left the note as it was', 'info');
  render();
}

function cleaned(draft, example) {
  const note = structuredClone(draft);
  note.example = example;
  for (const key of ['arabic', 'english', 'hint', 'example', 'example_en', 'comment', 'verb_form']) {
    if (typeof note[key] === 'string') note[key] = note[key].trim();
  }
  note.forms = (note.forms || [])
    .map((f) => ({ label: f.label.trim(), arabic: f.arabic.trim() }))
    .filter((f) => f.label || f.arabic);
  if (!note.forms.length) delete note.forms;
  return note;
}

async function saveEdit() {
  const example = composeExample();
  if (example === null) {
    say('The highlighted word has to appear in the sentence', 'bad');
    return;
  }
  let res;
  try {
    res = await post('save', cleaned(state.draft, example));
  } catch (err) {
    failed(err);
    return;
  }
  state.mode = 'review';
  state.draft = null;
  state.parts = null;
  if (res.message) say(res.message, res.tone || 'info');
  await load(res.show);
}

function actions() {
  const v = state.view;
  if (!v || state.finished) return [];
  if (state.mode === 'edit') {
    return [
      { key: 'ctrl+enter', label: 'Save changes', run: saveEdit, primary: true },
      { key: 'esc', label: 'Cancel', run: cancelEdit },
    ];
  }
  const list = [];
  if (!v.entry) {
    const first = v.list.find((x) => x.state === 'open');
    if (first) list.push({ key: 'enter', label: 'First open note', run: () => go(first.index), primary: true, nav: true });
    if (v.list.length) list.push({ key: 'left', label: 'Back to the notes', run: () => move(-1), nav: true });
    if (v.history) list.push({ key: 'u', label: 'Undo', run: () => act('undo') });
    return list;
  }
  const e = v.entry;
  if (e.proposal) {
    list.push(
      { key: 'y', label: 'Use this version', run: () => act('use'), primary: true, tone: 'claude' },
      { key: 'n', label: 'Keep yours', run: () => act('drop') },
      { key: 'e', label: 'Edit it first', run: () => startEdit(true) },
    );
  } else {
    list.push(
      { key: 'enter', label: e.state === 'open' ? 'Card is right' : 'Next open note', run: () => act('keep'), primary: true },
      { key: 'e', label: 'Edit', run: () => startEdit(false) },
    );
    (e.fixes || []).forEach((f, k) => {
      list.push({
        key: String(k + 1),
        label: FIX_LABEL[f.source] + (f.words.length > 1 ? 's' : ''),
        arabic: f.words.join(' '),
        run: () => act('ending', { source: f.source }),
        tone: 'good',
      });
    });
    if (e.state === 'open' || e.state === 'kept') {
      if (e.asking) {
        list.push({ key: 'esc', label: 'Stop Claude Code', run: () => act('stop'), tone: 'claude' });
      } else if (v.claude) {
        list.push({ key: 'c', label: 'Ask Claude', run: () => act('ask'), tone: 'claude' });
      }
    }
  }
  for (const kind of Object.keys(PLAY_KEY)) {
    if (ready(e, kind)) list.push({ key: PLAY_KEY[kind], label: 'Play ' + kind, run: () => play(kind), tone: 'audio' });
  }
  list.push(
    { key: 'left', label: 'Previous', run: () => move(-1), push: true, nav: true },
    { key: 'right', label: 'Next', run: () => move(1), nav: true },
  );
  if (v.history) list.push({ key: 'u', label: 'Undo', run: () => act('undo') });
  return list;
}

function keyOf(ev) {
  if (ev.altKey || ev.isComposing) return '';
  if (ev.code === 'Enter' || ev.code === 'NumpadEnter' || ev.key === 'Enter') {
    return ev.ctrlKey || ev.metaKey ? 'ctrl+enter' : 'enter';
  }
  if (ev.ctrlKey || ev.metaKey) return '';
  if (ev.key === 'Escape') return 'esc';
  if (ev.key === 'ArrowLeft') return 'left';
  if (ev.key === 'ArrowRight') return 'right';
  if (ev.code && ev.code.startsWith('Key')) return ev.code.slice(3).toLowerCase();
  if (/^(Digit|Numpad)\d$/.test(ev.code || '')) return ev.code.slice(-1);
  return '';
}

document.addEventListener('keydown', (ev) => {
  const key = keyOf(ev);
  if (!key) return;
  const typing = ev.target instanceof Element && ev.target.closest('input, textarea, select');
  if (typing && key !== 'esc' && key !== 'ctrl+enter') return;
  const action = actions().find((a) => a.key === key);
  if (!action) return;
  ev.preventDefault();
  run(action);
});

function run(action) {
  if (state.pending && !action.nav) return;
  action.run();
}

function render() {
  renderChrome();
  renderStage();
}

function renderChrome() {
  renderTop();
  renderList();
  renderFooter();
}

function renderTop() {
  const v = state.view;
  if (!v) return;
  const s = state.summary || v.summary;
  $('where').textContent = state.current >= 0 && !state.finished ? 'note ' + (state.current + 1) + ' of ' + s.total : 'summary';
  const counts = [el('span', null, s.open + ' open')];
  if (s.kept) counts.push(el('span', 'good', '✓ ' + s.kept + ' right'));
  if (s.edited) counts.push(el('span', 'good', '✎ ' + s.edited + ' edited'));
  if (s.rewritten) counts.push(el('span', 'claude', '✦ ' + s.rewritten + ' rewritten'));
  $('counts').replaceChildren(...counts);
  $('fill').style.width = (s.total ? (100 * (s.total - s.open)) / s.total : 100) + '%';
  $('finish').hidden = state.finished;
  const e = v.entry;
  document.title = (e && !state.finished ? e.note.arabic + ' · ' : '') + 'arabic-vocab review';
}

function icon(item) {
  if (item.asking) return el('span', 'icon spinner');
  if (item.ready) return el('span', 'icon claude', '✦');
  if (item.state === 'kept') return el('span', 'icon good', '✓');
  if (item.state === 'edited') return el('span', 'icon good', '✎');
  if (item.state === 'rewritten') return el('span', 'icon claude', '✦');
  return el('span', 'icon dot tone-' + item.tone);
}

function renderList() {
  const v = state.view;
  if (!v) return;
  const ol = el('ol');
  for (const item of v.list) {
    const button = el('button', 'item ' + item.state + (item.index === state.current ? ' current' : ''));
    button.type = 'button';
    button.title = item.english + ' · ' + plural(item.flags, 'flag', 'flags');
    button.append(icon(item), arabic('word', [{ t: item.arabic }]), el('span', 'num', '#' + item.position));
    button.addEventListener('click', () => go(item.index));
    ol.append(el('li', null, button));
  }
  const open = v.list.filter((x) => x.state === 'open').length;
  const heading = v.all ? 'Every note' : 'Flagged notes';
  $('list').replaceChildren(el('h2', null, el('span', null, heading), el('span', null, open + ' / ' + v.list.length)), ol);
  const current = $('list').querySelector('.current');
  if (current) current.scrollIntoView({ block: 'nearest' });
}

function renderFooter() {
  $('actions').replaceChildren(...actions().map(actionButton));
  renderStatus();
}

function actionButton(a) {
  let cls = 'action';
  if (a.primary) cls += ' primary';
  if (a.tone) cls += ' tone-' + a.tone;
  if (a.push) cls += ' push';
  const button = el('button', cls, el('kbd', null, KEY_LABEL[a.key] || a.key), el('span', null, a.label), a.arabic ? arabic('', [{ t: a.arabic }]) : null);
  button.type = 'button';
  button.addEventListener('click', () => run(a));
  return button;
}

function renderStatus() {
  const message = $('message');
  message.className = 'message ' + state.message.tone;
  message.textContent = state.message.text;
  const parts = [];
  if (state.playing) parts.push(el('span', 'playing', '▶ playing the ' + state.playing));
  const v = state.view;
  if (v && !state.finished) {
    const here = v.list.find((x) => x.index === state.current && x.asking);
    const others = v.list.filter((x) => x.asking && x.index !== state.current).length;
    if (here) parts.push(el('span', 'asking', el('span', 'spinner'), 'Claude Code is writing a new version'));
    if (others) parts.push(el('span', 'asking', el('span', 'spinner'), 'Claude Code is working on ' + plural(others, 'other note', 'other notes')));
  }
  $('activity').replaceChildren(...parts);
}

function renderStage() {
  const stage = $('stage');
  const v = state.view;
  if (!v) return;
  if (state.finished) {
    stage.replaceChildren(finishedView());
    return;
  }
  if (!v.entry) {
    stage.replaceChildren(doneView());
    stage.focus({ preventScroll: true });
    return;
  }
  const e = v.entry;
  let side;
  if (state.mode === 'edit') {
    side = editorView();
  } else if (e.proposal) {
    side = proposalView(e);
  } else {
    side = flagsView(e);
  }
  const card = cardView(state.mode === 'edit' ? previewCard() : e.card);
  stage.replaceChildren(card, side);
  if (state.shown !== e.index) {
    stage.scrollTop = 0;
    state.shown = e.index;
  }
  if (state.mode !== 'edit') stage.focus({ preventScroll: true });
}

function sourceLink(url) {
  const a = el('a', null, 'Wiktionary ↗');
  a.href = url;
  a.target = '_blank';
  a.rel = 'noreferrer noopener';
  return a;
}

function cardView(c) {
  const meta = el('div', 'meta', c.meta);
  if (c.source) meta.append(' · ', sourceLink(c.source));
  const card = el('section', 'card',
    el('div', 'card-head', el('div', 'headword', arabic('word', c.head)), meta),
    el('div', 'meaning', c.english || '—'));
  if (c.hint) card.append(el('div', 'hint', c.hint));
  if (c.forms && c.forms.length) {
    const forms = el('div', 'forms');
    for (const f of c.forms) forms.append(el('span', 'form', el('span', 'lbl', f.label), arabic('', f.spans)));
    card.append(forms);
  }
  card.append(el('div', 'example',
    el('div', 'sentence-row', arabic('sentence', c.example)),
    el('div', 'translation', c.example_en)));
  if (c.comment) card.append(el('div', 'comment', c.comment));
  return card;
}

function previewCard() {
  const d = state.draft;
  const meta = [POS_LABEL[d.pos] || d.pos];
  if (GENDER_LABEL[d.gender]) meta.push(GENDER_LABEL[d.gender]);
  if (d.verb_form) meta.push('form ' + d.verb_form);
  meta.push('#' + d.position);
  return {
    head: [{ t: d.arabic || '' }],
    meta: meta.join(' · '),
    source: d.source,
    english: d.english,
    hint: d.hint,
    forms: (d.forms || []).filter((f) => f.label || f.arabic).map((f) => ({ label: f.label, spans: [{ t: f.arabic }] })),
    example: markupSpans(composeExample() ?? state.parts.sentence),
    example_en: d.example_en,
    comment: d.comment,
  };
}

function markupSpans(text) {
  const out = [];
  let bold = false;
  for (const part of text.split(/(<\/?b>)/i)) {
    if (/^<b>$/i.test(part)) {
      bold = true;
    } else if (/^<\/b>$/i.test(part)) {
      bold = false;
    } else if (part) {
      out.push({ t: part.replace(/<[^>]*>/g, ''), c: bold ? 'target' : '' });
    }
  }
  return out;
}

function banner(tone, ...kids) {
  return el('div', 'banner tone-' + tone, ...kids);
}

function flagsView(e) {
  const side = el('section', 'side');
  const titles = {
    open: e.flags.length ? plural(e.flags.length, 'flag', 'flags') + ' to look at' : 'Nothing is flagged on this note',
    kept: '✓ You marked this card as right',
    edited: '✎ You edited this note',
    rewritten: '✦ You kept Claude Code\'s version',
  };
  side.append(el('h2', 'side-title ' + e.state, titles[e.state]));
  if (e.state === 'edited' || e.state === 'rewritten') {
    side.append(el('p', 'side-note', '\'arabic-vocab check\' will check it again; the flags below are from before the change.'));
  } else if (e.stale) {
    side.append(el('p', 'side-note', '\'arabic-vocab check\' has not seen this note since it changed, so nothing below is up to date.'));
  }
  if (e.asking) {
    side.append(banner('claude', el('span', 'spinner'),
      el('span', 'grow', 'Claude Code is writing a new version · ' + e.seconds + 's'),
      actionButton({ key: 'esc', label: 'Stop', run: () => act('stop'), tone: 'claude' })));
  }
  if (e.notice) side.append(banner(e.notice_tone === 'bad' ? 'bad' : 'info', el('span', 'grow', e.notice)));
  for (const f of e.flags) side.append(flagView(f));
  if (state.view.audio) side.append(clipsView(e));
  return side;
}

function clipsView(e) {
  const v = state.view;
  const box = el('section', 'clips', el('h3', null, 'Audio'));
  for (const c of e.clips) {
    const row = el('div', 'clip', el('span', 'kind', c.kind));
    row.append(
      clipButton(c.ready ? '↻ Remake' : '↻ Make', 'Synthesize this clip again with Google Text-to-Speech', true, () => remake(c.kind)),
      clipButton('✖ Remove', 'Delete this MP3', c.ready, () => removeClip(c.kind)));
    if (!c.ready) row.append(el('span', 'note', 'not made yet'));
    box.append(row);
  }
  if (v.voice) box.append(voiceRow(v.voice));
  if (!state.voices) loadVoices();
  return box;
}

function voiceLabel(v) {
  const name = v.name.replace(/^ar-XA-/, '');
  return v.gender ? name + ' · ' + v.gender.toLowerCase() : name;
}

function voiceRow(current) {
  const known = state.voices || [];
  const select = el('select');
  const options = known.length ? known.slice() : [{ name: current }];
  if (!options.some((o) => o.name === current)) options.unshift({ name: current });
  for (const o of options) {
    const option = el('option', null, voiceLabel(o));
    option.value = o.name;
    select.append(option);
  }
  select.value = current;
  select.disabled = !known.length;
  select.title = current;
  select.addEventListener('change', () => act('voice', { voice: select.value }));
  const row = el('div', 'clip', el('span', 'kind', 'voice'), select);
  if (state.voicesNote) row.append(el('span', 'note', state.voicesNote));
  return row;
}

async function loadVoices() {
  state.voices = [];
  let body;
  try {
    body = await request('api/voices');
  } catch (err) {
    if (!err.status) {
      failed(err);
      return;
    }
    state.voicesNote = err.message;
    body = null;
  }
  if (body) {
    state.voices = body.voices || [];
    state.voicesNote = state.voices.length ? '' : 'no voices to choose from';
  }
  if (state.mode !== 'edit' && !state.finished) renderStage();
}

function clipButton(label, title, enabled, run) {
  const button = el('button', 'small', label);
  button.type = 'button';
  button.title = title;
  button.disabled = !enabled;
  button.addEventListener('click', run);
  return button;
}

function flagView(f) {
  const block = el('article', 'flag tone-' + f.tone + (f.faded ? ' faded' : ''),
    el('header', null, el('span', 'symbol', f.symbol), el('h3', null, f.title), el('span', 'where', f.where)));
  if (f.rows.length) {
    const rows = el('dl');
    for (const r of f.rows) {
      const value = el('dd');
      if (r.spans.length) value.append(r.rtl ? arabic('value', r.spans) : el('span', 'value', spans(r.spans)));
      if (r.note) value.append(el('span', 'note', r.note));
      rows.append(el('dt', null, r.label), value);
    }
    block.append(rows);
  }
  if (f.explain && !f.faded) block.append(el('p', 'explain', f.explain));
  return block;
}

function proposalView(e) {
  const side = el('section', 'side', el('h2', 'side-title claude', '✦ Claude Code suggests this version'));
  for (const c of e.changes) {
    side.append(el('article', 'change', el('h3', null, c.label), changeLine('old', '−', c.old, c.rtl), changeLine('new', '+', c.new, c.rtl)));
  }
  side.append(el('p', 'side-note', 'Fields that did not change are not shown.'));
  return side;
}

function changeLine(kind, sign, list, rtl) {
  let text;
  if (!list || !list.length) {
    text = el('span', 'text none', '(none)');
  } else if (rtl) {
    text = arabic('text', list);
  } else {
    text = el('span', 'text', spans(list));
  }
  return el('div', 'line ' + kind, el('span', 'sign', sign), text);
}

function refreshPreview() {
  const card = $('stage').querySelector('.card');
  if (card) card.replaceWith(cardView(previewCard()));
}

function bind(node, key, target) {
  const obj = target || state.draft;
  node.value = obj[key] || '';
  node.addEventListener('input', () => {
    obj[key] = node.value;
    refreshPreview();
  });
  return node;
}

function textInput(key, rtl, target) {
  const node = bind(el('input', rtl ? 'ar-input' : ''), key, target);
  node.type = 'text';
  if (rtl) node.dir = 'rtl';
  return node;
}

function textArea(key, rtl, rows, target) {
  const node = el('textarea', rtl ? 'ar-input' : '');
  node.rows = rows;
  if (rtl) node.dir = 'rtl';
  return bind(node, key, target);
}

function sentenceFields() {
  if (!state.parts) {
    return [field('Sentence', textArea('example', true, 2), 'Put the word itself in <b>…</b>; the card on the left shows how it will look.')];
  }
  return [
    field('Sentence', textArea('sentence', true, 2, state.parts)),
    field('Word to highlight', textInput('word', true, state.parts), 'The form of the word as it appears in the sentence; the card shows it in colour.'),
  ];
}

function choice(key, options) {
  const node = el('select');
  for (const [value, label] of options) {
    const option = el('option', null, label);
    option.value = value;
    node.append(option);
  }
  if (state.draft[key] && !options.some(([value]) => value === state.draft[key])) {
    const option = el('option', null, state.draft[key]);
    option.value = state.draft[key];
    node.append(option);
  }
  return bind(node, key);
}

let fieldCount = 0;

function field(label, control, help) {
  fieldCount += 1;
  const id = 'field-' + fieldCount;
  const box = el('div', 'field');
  if (control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement || control instanceof HTMLSelectElement) {
    control.id = id;
    const lab = el('label', null, label);
    lab.htmlFor = id;
    box.append(lab, control);
  } else {
    box.append(el('span', 'label', label), control);
  }
  if (help) box.append(el('span', 'help', help));
  return box;
}

function formsEditor() {
  const box = el('div', 'pairs');
  const draw = () => {
    box.replaceChildren();
    state.draft.forms.forEach((f, i) => {
      const label = el('input');
      label.type = 'text';
      label.value = f.label;
      label.placeholder = 'pl.';
      label.addEventListener('input', () => {
        f.label = label.value;
        refreshPreview();
      });
      const word = el('input', 'ar-input');
      word.type = 'text';
      word.dir = 'rtl';
      word.value = f.arabic;
      word.addEventListener('input', () => {
        f.arabic = word.value;
        refreshPreview();
      });
      const remove = el('button', 'small', '×');
      remove.type = 'button';
      remove.title = 'Remove this form';
      remove.addEventListener('click', () => {
        state.draft.forms.splice(i, 1);
        draw();
        refreshPreview();
      });
      box.append(el('div', 'pair', label, word, remove));
    });
    const add = el('button', 'small', '+ Add a form');
    add.type = 'button';
    add.addEventListener('click', () => {
      state.draft.forms.push({ label: '', arabic: '' });
      draw();
    });
    box.append(add);
  };
  draw();
  return box;
}

function editorView() {
  const form = el('form', 'side editor');
  form.addEventListener('submit', (ev) => {
    ev.preventDefault();
    saveEdit();
  });
  form.append(
    el('h2', 'side-title', '✎ Edit the note'),
    field('Word', textInput('arabic', true)),
    el('div', 'grid3',
      field('Type', choice('pos', POS.map((p) => [p, POS_LABEL[p] || p]))),
      field('Gender', choice('gender', [['', '—'], ['m', 'masculine'], ['f', 'feminine'], ['m+f', 'both']])),
      field('Verb form', textInput('verb_form', false))),
    field('Meaning', textInput('english', false)),
    field('Hint', textInput('hint', false)),
    field('Forms', formsEditor()),
    ...sentenceFields(),
    field('Translation', textArea('example_en', false, 2)),
    field('Comment', textArea('comment', false, 2)),
  );
  return form;
}

function tally(s) {
  const rows = el('div', 'tally');
  const add = (n, symbol, cls, text) => {
    if (!n) return;
    rows.append(el('span', 'n', String(n)), el('span', 'sym ' + cls, symbol), el('span', null, text));
  };
  add(s.kept, '✓', 'icon good', 'right as they were');
  add(s.edited, '✎', 'icon good', 'edited by you');
  add(s.rewritten, '✦', 'icon claude', 'rewritten by Claude Code');
  add(s.open, '○', '', 'still open');
  return rows;
}

function notesWord(n) {
  return state.view && state.view.all ? plural(n, 'note', 'notes') : plural(n, 'flagged note', 'flagged notes');
}

function doneView() {
  const v = state.view;
  const s = v.summary;
  const done = s.open === 0;
  const box = el('section', 'done',
    el('h2', done ? 'all' : '', done ? '✓ All ' + notesWord(s.total) + ' are done' : (s.total - s.open) + ' of ' + notesWord(s.total) + ' are done'),
    tally(s));
  if (v.next) box.append(el('p', 'next', v.next));
  return box;
}

function finishedView() {
  const s = state.summary || state.view.summary;
  const box = el('section', 'finished', el('h2', null, state.ended ? 'The review has ended' : 'Review finished'), tally(s));
  box.append(el('p', 'next', state.ended
    ? 'The review was stopped in the terminal, which printed the summary. You can close this tab.'
    : 'The summary is in your terminal too. You can close this tab.'));
  return box;
}

$('finish').addEventListener('click', finish);
load(0);
