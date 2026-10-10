(() => {
  const tabs = [...document.querySelectorAll('[data-section]')];
  const select = tab => tabs.forEach(t => {
    const active = t === tab;
    t.setAttribute('aria-selected', String(active));
    t.tabIndex = active ? 0 : -1;
    document.getElementById(t.dataset.section).hidden = !active;
  });
  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => select(tab));
    tab.addEventListener('keydown', event => {
      let next;
      if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
      if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length;
      if (event.key === 'Home') next = 0;
      if (event.key === 'End') next = tabs.length - 1;
      if (next !== undefined) { event.preventDefault(); select(tabs[next]); tabs[next].focus(); }
    });
  });
  if (tabs.length) select(tabs[0]);
  document.getElementById('copy-digest')?.addEventListener('click', async () => {
    const text = document.getElementById('digest-markdown');
    const status = document.getElementById('copy-status');
    try {
      await navigator.clipboard.writeText(text.value);
      status.textContent = 'Скопійовано';
    } catch {
      text.hidden = false; text.focus(); text.select();
      status.textContent = 'Скопіюйте виділений текст вручну';
    }
  });
})();
