/**
 * 机器人头像：抽象的小鲸鱼，沿用 dsh-ee 标志（WhaleMark）的轮廓。
 *
 * - 按机器人 id 稳定取一组配色，同一个机器人永远是同一条鲸鱼，不同机器人靠颜色区分；
 * - active（正在回复）时鲸鱼轻轻游动、喷水，外圈有一圈淡淡的波纹；
 *   这些动画只在 prefers-reduced-motion: no-preference 下生效（见 index.css 的 bc-* 类）。
 * - 纯装饰：名字由旁边的文字给出，这里 aria-hidden。
 */

/** [底色, 鲸鱼, 肚皮高光]：底色都是浅色，鲸鱼取同色系的深色，深浅主题下都看得清。 */
const WHALE_COLORS: ReadonlyArray<readonly [string, string, string]> = [
  ["#e0e9ff", "#3b5bdb", "#a5b8fc"], // 品牌蓝
  ["#d5f5f0", "#0f766e", "#7dd3c4"], // 青
  ["#e4e1ff", "#4f46e5", "#b4adfb"], // 靛
  ["#dcf0fd", "#0369a1", "#8cc8ee"], // 天蓝
  ["#efe3ff", "#7c3aed", "#c9a8fb"], // 紫
  ["#dcf5e4", "#15803d", "#8bd5a3"], // 绿
  ["#fdf0d5", "#b45309", "#f3c37a"], // 琥珀
  ["#fde2e7", "#be185d", "#f3a3bf"], // 玫红
];

function hashOf(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

export function whaleColors(seed: string | number) {
  const [bg, body, belly] = WHALE_COLORS[hashOf(String(seed)) % WHALE_COLORS.length];
  return { bg, body, belly };
}

export function BotWhale({ seed, size, active = false, className }: { seed: string | number; size: number; active?: boolean; className?: string }) {
  const { bg, body, belly } = whaleColors(seed);
  return (
    <span
      aria-hidden
      data-whale-avatar=""
      data-active={active ? "true" : "false"}
      className={["relative inline-flex shrink-0", className].filter(Boolean).join(" ")}
      style={{ width: size, height: size }}
    >
      {active && <span className="bc-ring pointer-events-none absolute inset-0 rounded-full" style={{ boxShadow: `0 0 0 2px ${body}` }} />}
      <svg viewBox="0 0 24 24" width={size} height={size} focusable="false" className="relative">
        <circle cx="12" cy="12" r="12" fill={bg} />
        <g transform="translate(3.1 3.6) scale(0.74)">
          <g className={active ? "bc-swim" : undefined}>
            <path
              fill={body}
              fillRule="evenodd"
              d="M23 14.8c0 3.6-3 6.2-7.2 6.2H9.8c-3.4 0-5.6-2-5.9-5.1l-.3-4.6C2.5 10.9 1.4 9.9 1 8.4c1.6.2 2.8.7 3.5 1.7.7-1 1.9-1.5 3.5-1.7-.4 1.5-1.5 2.5-2.6 2.9l.1 1.4c.4 1.4 1.3 2.1 2.6 2.1C9.2 11.3 12.1 8.9 15.7 8.9c4.4 0 7.3 2.5 7.3 5.9Zm-3.3-.9a1.15 1.15 0 1 0-2.3 0 1.15 1.15 0 0 0 2.3 0Z"
            />
            {/* 肚皮：身体下沿的一道浅色弧 */}
            <path d="M8.6 19.2c2.4 1.2 6.6 1.3 9.6.2 1.7-.6 3-1.6 3.8-2.9-2.4 1.5-6 2-9.4 1.6-1.6-.2-3-.5-4-1.1Z" fill={belly} />
            <path
              className={active ? "bc-spout" : undefined}
              fill="none"
              stroke={body}
              strokeWidth="1.6"
              strokeLinecap="round"
              d="M15.8 7.8V6m0 0c-.3-1.5-1.4-2.2-2.7-2m2.7 2c.3-1.5 1.4-2.2 2.7-2"
            />
          </g>
        </g>
      </svg>
    </span>
  );
}
