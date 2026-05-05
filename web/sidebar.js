/* sidebar.js — Gentelella sidebar (inline, no ES module imports) */
(function () {
  'use strict';

  const SIDEBAR_STATE_KEY = 'gentelella:sidebar-collapsed';

  function readCollapsed() {
    try { return localStorage.getItem(SIDEBAR_STATE_KEY) === '1'; } catch { return false; }
  }
  function writeCollapsed(v) {
    try { localStorage.setItem(SIDEBAR_STATE_KEY, v ? '1' : '0'); } catch {}
  }

  function slideDown(el) {
    el.style.display = 'block';
    const h = el.scrollHeight;
    el.style.overflow = 'hidden';
    el.style.height = '0';
    el.style.transition = 'height 0.25s ease';
    requestAnimationFrame(() => { el.style.height = h + 'px'; });
    el.addEventListener('transitionend', function cb() {
      el.style.height = '';
      el.style.overflow = '';
      el.style.transition = '';
      el.removeEventListener('transitionend', cb);
    });
  }

  function slideUp(el) {
    el.style.overflow = 'hidden';
    el.style.height = el.scrollHeight + 'px';
    el.style.transition = 'height 0.25s ease';
    requestAnimationFrame(() => { el.style.height = '0'; });
    el.addEventListener('transitionend', function cb() {
      el.style.display = 'none';
      el.style.height = '';
      el.style.overflow = '';
      el.style.transition = '';
      el.removeEventListener('transitionend', cb);
    });
  }

  function initSidebar() {
    const body = document.body;
    const sidebarMenu = document.getElementById('sidebar-menu');
    if (!sidebarMenu) return;

    const collapseSidebar = () => {
      body.classList.remove('nav-md');
      body.classList.add('nav-sm');
    };
    const expandSidebar = () => {
      body.classList.remove('nav-sm');
      body.classList.add('nav-md');
    };

    if (readCollapsed()) collapseSidebar();

    // Menu item clicks
    sidebarMenu.addEventListener('click', function (ev) {
      const link = ev.target.closest('a');
      if (!link) return;
      const li = link.parentElement;
      const submenu = li.querySelector('ul.child_menu');
      if (!submenu) return;

      ev.preventDefault();
      ev.stopPropagation();

      const isOpen = submenu.style.display === 'block' || submenu.offsetHeight > 0;

      // Close siblings
      const parentUl = li.parentElement;
      if (parentUl) {
        [...parentUl.querySelectorAll(':scope > li')].forEach(sib => {
          if (sib !== li) {
            const sibSub = sib.querySelector('ul.child_menu');
            if (sibSub && sibSub.offsetHeight > 0) {
              slideUp(sibSub);
              sib.classList.remove('active');
            }
          }
        });
      }

      if (isOpen) {
        slideUp(submenu);
        li.classList.remove('active');
      } else {
        slideDown(submenu);
        li.classList.add('active');
      }
    });

    // Toggle button
    const menuToggle = document.getElementById('menu_toggle');
    if (menuToggle) {
      menuToggle.addEventListener('click', function (ev) {
        ev.preventDefault();
        const willCollapse = body.classList.contains('nav-md');
        if (willCollapse) { collapseSidebar(); } else { expandSidebar(); }
        writeCollapsed(willCollapse);
      });
    }

    // Highlight active nav item by data-tab
    const activateTab = (tabName) => {
      sidebarMenu.querySelectorAll('a[data-tab]').forEach(a => {
        const li = a.parentElement;
        li.classList.toggle('active', a.dataset.tab === tabName);
        li.classList.toggle('current-page', a.dataset.tab === tabName);
      });
    };

    // Visuelle Aktivierung der Sidebar-Links; showTab() wird via app.js aufgerufen.
    sidebarMenu.querySelectorAll('a[data-tab]').forEach(a => {
      a.addEventListener('click', function (ev) {
        ev.preventDefault();
        activateTab(this.dataset.tab);
      });
    });

    // Activate connection tab on load
    activateTab('connection');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initSidebar);
  } else {
    initSidebar();
  }
})();
