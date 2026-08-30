// Dragging a card between columns is the same status move as the form on the
// task page: it goes through the API, is checked against the fingerprint the
// card was rendered with, and lands in git as a commit.
//
// This is an enhancement, not the mechanism. Without JavaScript the board is
// still a board and every task page still moves its own status.
(() => {
  const board = document.querySelector('.board');
  if (!board) return;

  // The token the server drew into this page. A change carries it back, which
  // is what tells the server the change came from a page it served.
  const token = document.querySelector('meta[name="csrf-token"]')?.content || '';

  /* ---------- feedback ---------- */

  let toastTimer;
  function toast(message, bad) {
    let el = document.querySelector('.toast');
    if (!el) {
      el = document.createElement('div');
      el.className = 'toast';
      el.setAttribute('role', 'status');
      document.body.append(el);
    }
    el.textContent = message;
    el.classList.toggle('bad', !!bad);
    el.classList.add('on');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('on'), bad ? 6000 : 3000);
  }

  /* ---------- the placeholder ---------- */

  // A gap that opens where the card would land. Without it a drag is a guess:
  // you find out where the card went only after letting go.
  const slot = document.createElement('div');
  slot.className = 'slot';

  function place(cards, y) {
    const others = [...cards.querySelectorAll('.card:not(.dragging)')];
    const below = others.find(card => y < card.getBoundingClientRect().top + card.offsetHeight / 2);
    if (below) {
      cards.insertBefore(slot, below);
    } else {
      cards.append(slot);
    }
    // "Nothing here" beside a landing place contradicts itself.
    cards.querySelector('.empty').toggleAttribute('hidden', true);
  }

  function clearSlot() {
    slot.remove();
    board.querySelectorAll('.column').forEach(c => c.classList.remove('over', 'barred'));
    recount();
  }

  // Which card the dropped one lands behind — a key, or "" for the top of the
  // column. A key rather than an index: between drawing this board and letting
  // go, somebody else may have added or moved a card, and "after ACME-4"
  // survives that where "third from the top" quietly means something else.
  function precedingKey(at) {
    let previous = at.previousElementSibling;
    while (previous && !previous.classList.contains('card')) {
      previous = previous.previousElementSibling;
    }
    return previous ? previous.dataset.key : '';
  }

  // The workflow, as the server rendered it onto the card.
  function reaches(card, column) {
    const allowed = (card.dataset.reachable || '').split('\n').filter(Boolean);
    return allowed.length === 0 || allowed.includes(column.dataset.status);
  }

  /* ---------- dragging ---------- */

  let dragged = null;
  let moved = false;

  board.querySelectorAll('.card').forEach(card => {
    card.draggable = true;

    card.addEventListener('dragstart', event => {
      dragged = card;
      moved = false;
      event.dataTransfer.setData('text/plain', card.dataset.key);
      event.dataTransfer.effectAllowed = 'move';
      // An anchor drags as a link by default, ghost URL and all. Naming the
      // card as the drag image makes it drag as the card it looks like.
      const box = card.getBoundingClientRect();
      event.dataTransfer.setDragImage(card, box.width / 2, 20);
      // Deferred, or the browser snapshots the half-transparent card.
      setTimeout(() => card.classList.add('dragging'), 0);
    });

    card.addEventListener('dragend', () => {
      card.classList.remove('dragging');
      clearSlot();
      dragged = null;
      // A drag that ended on the card it started from is not a click.
      setTimeout(() => { moved = false; }, 0);
    });

    card.addEventListener('click', event => {
      if (moved) event.preventDefault();
    });
  });

  board.querySelectorAll('.column').forEach(column => {
    const cards = column.querySelector('.cards');

    // dragover rather than dragenter: it keeps firing as the pointer moves, so
    // the placeholder follows the cursor instead of jumping between children.
    column.addEventListener('dragover', event => {
      if (!dragged) return;
      moved = true;

      // Refusing before the drop rather than after it. A column you cannot
      // drop into should look like one.
      if (!reaches(dragged, column)) {
        event.dataTransfer.dropEffect = 'none';
        column.classList.add('barred');
        // No landing place, because there is no landing.
        slot.remove();
        return;
      }
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      column.classList.add('over');
      place(cards, event.clientY);
    });

    column.addEventListener('dragleave', event => {
      // dragleave fires on every child boundary. Only the pointer actually
      // leaving the column counts.
      if (column.contains(event.relatedTarget)) return;
      column.classList.remove('over', 'barred');
    });

    column.addEventListener('drop', async event => {
      event.preventDefault();

      const card = dragged;
      const status = column.dataset.status;
      if (!card || !status) return clearSlot();
      if (!reaches(card, column)) {
        toast('The workflow does not allow ' + card.dataset.status + ' → ' + status, true);
        return clearSlot();
      }

      // Where the placeholder sits is where the card goes, in the column it
      // came from as much as in a new one: a column somebody has arranged is
      // arranged, and reloading the page should not undo it.
      const target = slot.parentNode ? slot : null;
      const after = precedingKey(target || card);
      const sameColumn = card.dataset.status === status;

      const key = card.dataset.key;
      card.classList.add('pending');
      if (target) target.replaceWith(card);
      clearSlot();
      recount();

      const change = { after, version: card.dataset.version };
      if (!sameColumn) change.status = status;

      try {
        const response = await fetch('/api/tasks/' + key, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token },
          body: JSON.stringify(change),
        });

        if (response.status === 409) {
          // Someone — Obsidian, an agent — changed the file after this page
          // was drawn. Reloading is the honest response: the board on screen
          // is out of date, not just this one card.
          toast(key + ' changed elsewhere. Reloading.', true);
          setTimeout(() => location.reload(), 900);
          return;
        }
        if (!response.ok) {
          const body = await response.json().catch(() => ({}));
          throw new Error(body.error || response.statusText);
        }

        const task = await response.json();
        card.dataset.status = task.status;
        card.dataset.version = task.version;
        // Where it may go next depends on where it is now. Without this a card
        // dragged twice is checked against the workflow it used to be under.
        card.dataset.reachable = (task.reachable || []).join('\n');
        toast(sameColumn ? key + ' moved in ' + status : key + ' → ' + task.status);
        // A reorder that ran out of room renumbers the column, which makes the
        // other cards' versions on this page stale. Nothing is done about that
        // here: dragging one of them is refused with a 409 and reloads, which
        // is the same answer as for any other change made behind this page's
        // back, and it costs nothing until it happens.
      } catch (error) {
        toast(error.message + ' — reloading', true);
        setTimeout(() => location.reload(), 1200);
      } finally {
        card.classList.remove('pending');
      }
    });
  });

  // The board itself, so a card dropped in the gutter goes back rather than
  // vanishing into the page.
  board.addEventListener('dragover', event => { if (dragged) event.preventDefault(); });
  board.addEventListener('drop', event => { event.preventDefault(); clearSlot(); });

  function recount() {
    board.querySelectorAll('.column').forEach(column => {
      const count = column.querySelectorAll('.card').length;
      column.querySelector('.count').textContent = count;
      column.querySelector('.empty').toggleAttribute('hidden', count > 0);
    });
  }
})();
