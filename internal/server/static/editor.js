// A Markdown editor: a toolbar, a preview, and nothing that rewrites what you
// typed behind your back.
//
// The preview is rendered by the server, by the same code that renders the
// page you are editing. A preview drawn by a second, client-side Markdown
// implementation would disagree with the real one sooner or later, and a
// preview you cannot trust is worse than none.
(() => {
  function setUp(textarea) {
    const editor = document.createElement('div');
    editor.className = 'editor';
    textarea.parentNode.insertBefore(editor, textarea);

    const bar = document.createElement('div');
    bar.className = 'editor-bar';
    editor.append(bar);

    const panes = document.createElement('div');
    panes.className = 'editor-panes';
    editor.append(panes);
    panes.append(textarea);

    const preview = document.createElement('div');
    preview.className = 'editor-preview body';
    preview.hidden = true;
    panes.append(preview);

    for (const tool of tools) {
      if (tool.gap) {
        bar.append(Object.assign(document.createElement('span'), { className: 'editor-gap' }));
        continue;
      }
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'editor-tool';
      button.textContent = tool.label;
      button.title = tool.title;
      button.addEventListener('click', () => { tool.run(textarea); textarea.focus(); });
      bar.append(button);
    }

    const toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'editor-tool editor-preview-toggle';
    toggle.textContent = 'Preview';
    toggle.title = 'Show the page as it will be rendered';
    bar.append(toggle);

    let showing = false;
    toggle.addEventListener('click', async () => {
      showing = !showing;
      toggle.classList.toggle('on', showing);
      preview.hidden = !showing;
      editor.classList.toggle('split', showing);
      if (showing) await render(textarea, preview);
    });

    let debounce;
    textarea.addEventListener('input', () => {
      if (!showing) return;
      clearTimeout(debounce);
      debounce = setTimeout(() => render(textarea, preview), 400);
    });

    // Tab indents rather than leaving the field: in a Markdown editor a list
    // that cannot be nested is a list you finish somewhere else.
    textarea.addEventListener('keydown', event => {
      if (event.key === 'Tab' && !event.metaKey && !event.ctrlKey) {
        event.preventDefault();
        wrap(textarea, '  ', '');
      }
      if ((event.metaKey || event.ctrlKey) && event.key === 'b') {
        event.preventDefault();
        wrap(textarea, '**', '**');
      }
      if ((event.metaKey || event.ctrlKey) && event.key === 'i') {
        event.preventDefault();
        wrap(textarea, '*', '*');
      }
    });
  }

  async function render(textarea, preview) {
    preview.classList.add('loading');
    try {
      const response = await fetch('/preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ body: textarea.value }),
      });
      preview.innerHTML = response.ok
        ? await response.text()
        : '<p class="empty">The preview could not be rendered.</p>';
    } catch (error) {
      preview.innerHTML = '<p class="empty">The preview could not be reached.</p>';
    } finally {
      preview.classList.remove('loading');
    }
  }

  /* ---------- the tools ---------- */

  const tools = [
    { label: 'B', title: 'Bold', run: t => wrap(t, '**', '**') },
    { label: 'I', title: 'Italic', run: t => wrap(t, '*', '*') },
    { label: '</>', title: 'Code', run: t => wrap(t, '`', '`') },
    { gap: true },
    { label: 'H2', title: 'Heading', run: t => prefixLines(t, '## ') },
    { label: '“”', title: 'Quote', run: t => prefixLines(t, '> ') },
    { label: '•', title: 'Bullet list', run: t => prefixLines(t, '- ') },
    { label: '1.', title: 'Numbered list', run: t => numberLines(t) },
    { label: '☐', title: 'Checklist', run: t => prefixLines(t, '- [ ] ') },
    { gap: true },
    { label: 'Link', title: 'Link', run: t => wrap(t, '[', '](https://)') },
    { label: '[[ ]]', title: 'Link to a note in this vault', run: t => wrap(t, '[[', ']]') },
    { label: '{ }', title: 'Code block', run: t => fence(t) },
    { label: '—', title: 'Divider', run: t => insert(t, '\n\n---\n\n') },
  ];

  function selection(t) {
    return { start: t.selectionStart, end: t.selectionEnd, text: t.value.slice(t.selectionStart, t.selectionEnd) };
  }

  function replace(t, start, end, text, caretStart, caretEnd) {
    t.setRangeText(text, start, end, 'end');
    if (caretStart !== undefined) t.setSelectionRange(caretStart, caretEnd ?? caretStart);
    t.dispatchEvent(new Event('input', { bubbles: true }));
  }

  function wrap(t, before, after) {
    const { start, end, text } = selection(t);
    replace(t, start, end, before + text + after,
      text ? start + before.length + text.length + after.length : start + before.length);
  }

  function insert(t, text) {
    const { start, end } = selection(t);
    replace(t, start, end, text);
  }

  // Whole lines, so a heading marks the line the cursor is on rather than
  // appearing in the middle of a word.
  function lineRange(t) {
    const { start, end } = selection(t);
    const from = t.value.lastIndexOf('\n', start - 1) + 1;
    let to = t.value.indexOf('\n', end);
    if (to < 0) to = t.value.length;
    return { from, to };
  }

  function prefixLines(t, prefix) {
    const { from, to } = lineRange(t);
    const lines = t.value.slice(from, to).split('\n');
    const already = lines.every(l => l.startsWith(prefix));
    const changed = lines.map(l => (already ? l.slice(prefix.length) : prefix + l)).join('\n');
    replace(t, from, to, changed, from, from + changed.length);
  }

  function numberLines(t) {
    const { from, to } = lineRange(t);
    const changed = t.value.slice(from, to).split('\n')
      .map((l, i) => `${i + 1}. ${l.replace(/^\d+\.\s*/, '')}`).join('\n');
    replace(t, from, to, changed, from, from + changed.length);
  }

  function fence(t) {
    const { start, end, text } = selection(t);
    const block = '```\n' + (text || '') + '\n```\n';
    replace(t, start, end, block, start + 4);
  }

  // Started here, at the bottom: the tool list below is a const, and calling
  // setUp above it would read it before it exists.
  document.querySelectorAll('textarea[data-editor]').forEach(setUp);
})();
