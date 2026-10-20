// Drafts stay only in the current page; no notes, PINs or form bodies are persisted.
export function hasUnsavedChecklist(): boolean {
  return document.querySelector('form[data-checklist-dirty="true"]') !== null;
}

export function confirmChecklistDiscard(): boolean {
  return !hasUnsavedChecklist() || window.confirm('Есть несохранённые ответы или замечания чек-листа. Они будут потеряны. Продолжить?');
}
