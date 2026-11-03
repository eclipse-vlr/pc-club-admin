export function ProgressMeter({ value, max, label }: { value: number; max: number; label: string }) {
  const ratio = max > 0 ? Math.max(0, Math.min(1, value / max)) : 0;
  return <><progress className="progress-native visually-hidden" aria-label={label} value={value} max={max || 1} /><span className="progress-meter" aria-hidden="true" data-complete={max > 0 && value >= max}><span style={{ transform: `scaleX(${ratio})` }} /></span></>;
}
