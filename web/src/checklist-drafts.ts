// Drafts stay only in the current page; no notes, PINs or form bodies are persisted.
export function hasUnsavedChecklist(): boolean {
  return document.querySelector('form[data-checklist-dirty="true"]') !== null;
}

export function hasUnsavedCash(except?: HTMLFormElement): boolean {
  return Array.from(document.querySelectorAll<HTMLFormElement>('form[data-cash-dirty="true"]')).some(form => form !== except);
}

export function hasUnsavedDrafts(): boolean {
  return hasUnsavedChecklist() || hasUnsavedCash();
}

export function confirmDraftDiscard(): boolean {
  return !hasUnsavedDrafts() || window.confirm('Есть несохранённые ответы, замечания или данные кассы. Они будут потеряны. Продолжить?');
}

export function confirmCashDiscard(except?: HTMLFormElement): boolean {
  return !hasUnsavedCash(except) || window.confirm('Есть несохранённые данные другой кассовой формы. Обновление смены удалит их. Продолжить?');
}
