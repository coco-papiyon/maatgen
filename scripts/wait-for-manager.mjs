const managerUrl = process.env.MAATGEN_MANAGER_URL ?? 'http://127.0.0.1:3100';
const timeoutMs = Number(process.env.MAATGEN_MANAGER_WAIT_MS ?? 30_000);
const intervalMs = 250;
const deadline = Date.now() + timeoutMs;

while (Date.now() < deadline) {
  try {
    const response = await fetch(`${managerUrl}/api/v1/health`, {
      signal: AbortSignal.timeout(1_000),
    });
    if (response.ok) {
      process.exit(0);
    }
  } catch {
    // The manager is still starting or its port is not ready yet.
  }
  await new Promise((resolve) => setTimeout(resolve, intervalMs));
}

console.error(`Agent Manager did not become ready at ${managerUrl} within ${timeoutMs}ms.`);
process.exit(1);
