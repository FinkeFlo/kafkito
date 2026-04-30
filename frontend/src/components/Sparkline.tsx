export function Sparkline({
  data,
  w = 80,
  h = 24,
  className,
  stroke = "currentColor",
}: {
  data: number[];
  w?: number;
  h?: number;
  className?: string;
  stroke?: string;
}) {
  if (!data.length) {
    return <svg width={w} height={h} className={className} />;
  }
  if (data.length === 1) {
    const y = h / 2;
    return (
      <svg width={w} height={h} className={className} style={{ color: "var(--color-accent)" }}>
        <line x1={0} y1={y} x2={w} y2={y} stroke={stroke} strokeWidth={1.5} strokeLinecap="round" />
      </svg>
    );
  }
  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;
  const d = data
    .map((v, i) => {
      const x = (i / (data.length - 1)) * w;
      const y = h - ((v - min) / range) * h;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg width={w} height={h} className={className} style={{ color: "var(--color-accent)" }}>
      <path
        d={d}
        fill="none"
        stroke={stroke}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
