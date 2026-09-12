'use strict';
const filter = document.querySelector('[data-filter]');
if (filter) {
  filter.closest('label').hidden = false;
  filter.addEventListener('input', () => {
    const query = filter.value.toLocaleLowerCase().trim();
    let visible = 0;
    document.querySelectorAll('[data-item]').forEach(item => {
      item.hidden = !item.textContent.toLocaleLowerCase().includes(query);
      if (!item.hidden) visible++;
    });
    document.querySelector('#no-results').hidden = visible > 0;
  });
}
const details = [...document.querySelectorAll('.transcript-detail')];
const toggle = document.querySelector('#details-toggle');
if (toggle && details.length) {
  toggle.hidden = false;
  const update = () => {
    toggle.textContent = details.every(detail => detail.open) ? 'Collapse details' : 'Expand details';
  };
  toggle.addEventListener('click', () => {
    const expand = details.some(detail => !detail.open);
    details.forEach(detail => { detail.open = expand; });
    update();
  });
  details.forEach(detail => detail.addEventListener('toggle', update));
}
const index = document.querySelector('.index-disclosure');
if (index && window.matchMedia('(max-width: 760px)').matches) index.open = false;
if (navigator.clipboard && window.isSecureContext) {
  document.querySelectorAll('[data-copy]').forEach(button => {
    button.hidden = false;
    button.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(button.closest('.code-block').querySelector('code').textContent);
        button.textContent = 'Copied';
      } catch {
        button.textContent = 'Select text to copy';
      }
      setTimeout(() => { button.textContent = 'Copy'; }, 2000);
    });
  });
}
