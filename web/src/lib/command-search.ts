// The command menu's search (#436): a fuzzy, typo-tolerant scorer and the
// ranking on top of it. Pure, so it's tested in node.
//
// Each word of the query has to match the item somewhere (its label or
// keywords). A word matches, best first: the whole text, the start of the
// text, the start of one of its words, the initials of its words ("mp" for
// "Minecraft Paper"), its letters in order with word starts and runs
// rewarded ("mcp" for "Minecraft Paper"), and last, a word one or two
// typos away ("survivl" for "Survival"). Recently used items and the page
// you're on rank higher.

// normalize lowercases and strips accents, so "Café" matches "cafe".
export function normalize(s: string): string {
  return s.normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

// words splits text into its words: runs of letters and digits.
export function words(s: string): string[] {
  return normalize(s).match(/[\p{L}\p{N}]+/gu) ?? [];
}

// Scores of the ways a query word can match, out of 100.
const exact = 100;
const prefix = 90;
const wordPrefix = 80;
const initials = 70;
const subsequenceMax = 64;
const typo = 30;

// subsequence scores the query's letters found in order in the text:
// letters at a word's start and letters right after the last match count
// more, gaps count against. 0: not all found. Dynamic programming over
// both strings, so the best alignment wins, not the first.
function subsequence(q: string, text: string): number {
  const n = q.length;
  const m = text.length;
  if (n === 0 || n > m) return 0;
  const start = (i: number) => i === 0 || !/[\p{L}\p{N}]/u.test(text[i - 1] ?? "");
  // best[j]: the best score with q[0..i] matched and q[i] at text[j].
  let prev: number[] = new Array(m).fill(-1);
  for (let i = 0; i < n; i++) {
    const cur: number[] = new Array(m).fill(-1);
    let bestBefore = -1; // the best prev[k] for k < j - 1
    for (let j = 0; j < m; j++) {
      if (j >= 2) bestBefore = Math.max(bestBefore, prev[j - 2] ?? -1);
      if (text[j] !== q[i]) continue;
      const letter = 1 + (start(j) ? 3 : 0);
      if (i === 0) {
        cur[j] = letter + (j === 0 ? 2 : 0);
        continue;
      }
      const run = j > 0 && (prev[j - 1] ?? -1) >= 0 ? (prev[j - 1] ?? 0) + letter + 2 : -1;
      const gap = bestBefore >= 0 ? bestBefore + letter - 0.5 : -1;
      cur[j] = Math.max(run, gap);
    }
    prev = cur;
  }
  const best = Math.max(...prev);
  if (best < 0) return 0;
  // The most a query this long could score: every letter a word start,
  // all in one run, at the very start.
  const most = n * 6 + 2;
  return Math.max(1, Math.round((subsequenceMax * best) / most));
}

// distance is the edit distance between two short strings, a swap of two
// neighbouring letters counting as one edit.
export function distance(a: string, b: string): number {
  const d: number[][] = [];
  for (let i = 0; i <= a.length; i++) d.push([i]);
  for (let j = 1; j <= b.length; j++) (d[0] as number[])[j] = j;
  for (let i = 1; i <= a.length; i++) {
    const row = d[i] as number[];
    const up = d[i - 1] as number[];
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      let v = Math.min(
        (up[j] as number) + 1,
        (row[j - 1] as number) + 1,
        (up[j - 1] as number) + cost,
      );
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) {
        v = Math.min(v, ((d[i - 2] as number[])[j - 2] as number) + 1);
      }
      row[j] = v;
    }
  }
  return (d[a.length] as number[])[b.length] as number;
}

// typos is how many a word of this length may have: none under 4 letters,
// where nearly everything is one edit from something.
function typosAllowed(len: number): number {
  return len < 4 ? 0 : len < 8 ? 1 : 2;
}

// wordScore is how well one query word (normalized) matches text.
export function wordScore(q: string, text: string): number {
  if (!q) return 0;
  const t = normalize(text);
  if (t === q) return exact;
  if (t.startsWith(q)) return prefix;
  const ws = words(t);
  const at = ws.findIndex((w) => w.startsWith(q));
  if (at >= 0) return wordPrefix - Math.min(at, 5);
  if (q.length >= 2 && q.length <= ws.length) {
    for (let i = 0; i + q.length <= ws.length; i++) {
      if (ws.slice(i, i + q.length).every((w, k) => w[0] === q[k]))
        return initials - Math.min(i, 5);
    }
  }
  const sub = subsequence(q, t);
  if (sub > 0) return sub;
  const allowed = typosAllowed(q.length);
  if (allowed > 0) {
    let best = Infinity;
    for (const w of ws) {
      // Against the whole word and against its start, so a typo in a word
      // still being typed counts.
      best = Math.min(best, distance(q, w), distance(q, w.slice(0, q.length)));
    }
    if (best <= allowed) return typo - 10 * (best - 1);
  }
  return 0;
}

// Searchable is what the scorer reads from an item.
export type Searchable = {
  label: string;
  // Other words it's found by: a server's address, egg, and node; an
  // action's synonyms.
  keywords?: readonly string[];
};

// matchScore is how well a whole query matches an item: every word has to
// match its label or a keyword (a keyword counts a little less). 0: no
// match. A label that starts with the whole query gets a bonus.
export function matchScore(query: string, item: Searchable): number {
  const qs = words(query);
  if (qs.length === 0) return 0;
  const keywords = item.keywords ?? [];
  let total = 0;
  for (const q of qs) {
    let best = wordScore(q, item.label);
    for (const k of keywords) best = Math.max(best, wordScore(q, k) * 0.85);
    if (best === 0) return 0;
    total += best;
  }
  let score = total / qs.length;
  const label = normalize(item.label);
  const whole = qs.join(" ");
  if (label.startsWith(whole)) score += 10;
  else if (qs.length > 1 && label.includes(whole)) score += 5;
  return score;
}

// Use is how often and how lately an item was picked (lib/command-recents).
export type Use = { count: number; last: number };

const day = 24 * 60 * 60 * 1000;

// recencyBoost favours what was used often and lately: up to 25 points,
// halving every week since it was last used.
export function recencyBoost(use: Use | undefined, now: number): number {
  if (!use || use.count <= 0) return 0;
  const age = Math.max(0, now - use.last) / day;
  const freq = Math.min(1, Math.log2(1 + use.count) / 4);
  return 25 * freq * 0.5 ** (age / 7);
}

export type Rankable = Searchable & {
  id: string;
  // Extra points: the page you're on (its server's actions), an item
  // that's usually wanted.
  boost?: number;
};

// rank is the items matching query, best first, with their scores. An
// empty query keeps them all, ordered by boost and use. Ties keep the
// items' own order.
export function rank<T extends Rankable>(
  items: readonly T[],
  query: string,
  uses: ReadonlyMap<string, Use> = new Map(),
  now = Date.now(),
): { item: T; score: number }[] {
  const empty = words(query).length === 0;
  const out: { item: T; score: number; i: number }[] = [];
  items.forEach((item, i) => {
    const m = empty ? 1 : matchScore(query, item);
    if (m <= 0) return;
    out.push({ item, score: m + (item.boost ?? 0) + recencyBoost(uses.get(item.id), now), i });
  });
  out.sort((a, b) => b.score - a.score || a.i - b.i);
  return out.map(({ item, score }) => ({ item, score }));
}
