// Dragging a card between columns is the same status move as the form on the
// task page: it goes through the API, is checked against the fingerprint the
// card was rendered with, and lands in git as a commit.
//
// This is an enhancement, not the mechanism. Without JavaScript the board is
// still a board and every task page still moves its own status.
(() => {
  const board = document.querySelector('.board');
  if (!board) return;

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
    toastTimer = setTimeout(() => el.classList.remove('on'), 4000);
  }

  let dragged = null;

  board.querySelectorAll('.card').forEach(card => {
    card.draggable = true;

    card.addEventListener('dragstart', event => {
      dragged = card;
      event.dataTransfer.setData('text/plain', card.dataset.key);
      event.dataTransfer.effectAllowed = 'move';
      card.classList.add('dragging');
    });

    card.addEventListener('dragend', () => {
      card.classList.remove('dragging');
      dragged = null;
    });

    // A card is a link, so a plain drag would otherwise start a link drag.
    card.addEventListener('click', event => {
      if (card.classList.contains('dragging')) event.preventDefault();
    });
  });

  board.querySelectorAll('.column').forEach(column => {
    column.addEventListener('dragover', event => {
      if (!dragged) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      column.classList.add('over');
    });

    column.addEventListener('dragleave', () => column.classList.remove('over'));

    column.addEventListener('drop', async event => {
      event.preventDefault();
      column.classList.remove('over');

      const card = dragged;
      if (!card) return;

      const status = column.dataset.status;
      if (!status || card.dataset.status === status) return;

      const key = card.dataset.key;
      card.classList.add('pending');

      try {
        const response = await fetch('/api/tasks/' + key, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ status, version: card.dataset.version }),
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
        column.querySelector('.cards').prepend(card);
        recount();
        toast(key + ' → ' + task.status);
      } catch (error) {
        toast(error.message, true);
      } finally {
        card.classList.remove('pending');
      }
    });
  });

  function recount() {
    board.querySelectorAll('.column').forEach(column => {
      const count = column.querySelectorAll('.card').length;
      column.querySelector('.count').textContent = count;
      column.querySelector('.empty')?.toggleAttribute('hidden', count > 0);
    });
  }
})();
