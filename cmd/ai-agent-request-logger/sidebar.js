/* "On this page" outline: links to the page's own sections and marks the one being read. */
(() => {
  "use strict";
  const nav = document.querySelector(".page-outline");
  if (!nav) return;
  const targets = [...document.querySelectorAll('main [id^="section-"], main a[id^="message-"]')];
  if (targets.length === 0) {
    nav.hidden = true;
    return;
  }
  const list = nav.querySelector("ol");
  const links = targets.map((target) => {
    const link = document.createElement("a");
    link.href = "#" + target.id;
    link.textContent = (target.closest("summary") || target).textContent.trim();
    const item = document.createElement("li");
    if (target.id === "section-system-prompt" || target.id === "section-tools") item.className = "nested";
    item.append(link);
    list.append(item);
    return link;
  });
  // A section counts as read once its top has scrolled past this many pixels from the viewport's top.
  const readingLine = 120;
  let frame = 0;
  const mark = () => {
    frame = 0;
    // Targets inside a closed box have no layout box, so they cannot be the section in view.
    const shown = targets
      .map((target, index) => ({ index, top: target.getClientRects().length > 0 ? target.getBoundingClientRect().top : null }))
      .filter((item) => item.top !== null);
    let current = 0;
    shown.forEach((item) => {
      if (item.top <= readingLine) current = item.index;
    });
    // At the page end, later sections cannot scroll up to the reading line, so the last section counts as read.
    const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2;
    if (atBottom && shown.length > 0) current = shown[shown.length - 1].index;
    links.forEach((link, index) => {
      if (index === current) link.setAttribute("aria-current", "true");
      else link.removeAttribute("aria-current");
    });
  };
  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(mark);
  };
  window.addEventListener("scroll", schedule, { passive: true });
  window.addEventListener("resize", schedule);
  // Opening or closing a box moves its contents without scrolling, so re-mark when a box toggles.
  document.addEventListener("toggle", schedule, true);
  mark();
})();
