// Poll the read-only card while text retrieval or digest generation is pending.
// Preserve any user input or expanded controls instead of reloading over them.
(() => {
  const panel = document.querySelector('#processing[data-pending="true"]') || document.querySelector('#knowledge[data-pending="true"]');
  if (!panel || panel.dataset.pending !== "true") return;
  const message = document.getElementById(panel.id+"-message");
  const refresh = document.getElementById(panel.id+"-refresh");
  let edited = false;
  document.addEventListener("input", () => { edited = true; });
  document.addEventListener("change", () => { edited = true; });

  async function poll() {
    if (document.hidden) {
      setTimeout(poll, 2000);
      return;
    }
    try {
      const response = await fetch(location.href, {cache: "no-store"});
      if (!response.ok) throw new Error("status unavailable");
      const page = new DOMParser().parseFromString(await response.text(), "text/html");
      const next = page.getElementById(panel.id);
      if (!next) throw new Error("card unavailable");
      if (next.dataset.pending === "false") {
        const focused = document.activeElement?.matches("input, textarea, select");
        if (!edited && !focused && !document.querySelector("details[open]")) {
          location.reload();
        } else {
          message.textContent = "Обробку завершено. Ваше введення залишилось на сторінці; відкрийте результат, коли будете готові.";
          refresh.hidden = false;
        }
        return;
      }
      message.textContent = page.getElementById(panel.id+"-message").textContent;
    } catch {
      message.textContent = "Не вдалося оновити стан. Посилання збережено; спробуйте оновити сторінку.";
      refresh.hidden = false;
    }
    setTimeout(poll, 2000);
  }
  setTimeout(poll, 2000);
})();
