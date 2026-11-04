/* Opens the folded boxes around a tool or message when its link is followed, then scrolls to it. */
(() => {
  "use strict";
  const reveal = (id) => {
    if (!id.startsWith("tool-") && !id.startsWith("message-")) return;
    const target = document.getElementById(id);
    if (!target) return;
    let box = target.closest("details");
    while (box) {
      box.open = true;
      box = box.parentElement ? box.parentElement.closest("details") : null;
    }
    target.scrollIntoView({ block: "start" });
  };
  document.addEventListener("click", (event) => {
    const link = event.target.closest('a[href^="#tool-"], a[href^="#message-"]');
    if (link) reveal(link.getAttribute("href").slice(1));
  });
  window.addEventListener("hashchange", () => reveal(location.hash.slice(1)));
  reveal(location.hash.slice(1));
})();
