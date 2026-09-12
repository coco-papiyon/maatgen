// Deterministic per-server color for the Session list's status dot and the
// cross-server aggregate view: every browser derives the same color for the
// same node id without any server round-trip or stored preference, so a
// server's color stays stable across reloads and across every operator's
// browser without needing to be configured anywhere.
const PALETTE = [
  '#57d47b', // green
  '#6fa8dc', // blue
  '#d7b868', // amber
  '#ff8f83', // red
  '#c78ee0', // purple
  '#4fd1c5', // teal
  '#e08a4f', // orange
  '#e08ac0', // pink
];

export function colorForNode(nodeId: string): string {
  let hash = 0;
  for (let index = 0; index < nodeId.length; index += 1) {
    hash = (hash * 31 + nodeId.charCodeAt(index)) | 0;
  }
  return PALETTE[Math.abs(hash) % PALETTE.length]!;
}
