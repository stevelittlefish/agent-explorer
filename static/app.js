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
const toggle = document.querySelector('#details-toggle');
if (toggle && document.querySelector('details')) {
  toggle.hidden = false;
  toggle.addEventListener('click', () => {
    const expand = [...document.querySelectorAll('details')].some(detail => !detail.open);
    document.querySelectorAll('details').forEach(detail => { detail.open = expand; });
    toggle.textContent = expand ? 'Collapse details' : 'Expand details';
  });
}
