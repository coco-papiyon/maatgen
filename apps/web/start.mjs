import { createReadStream } from 'node:fs';
import { access, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, normalize, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const packageDirectory = fileURLToPath(new URL('.', import.meta.url));
const distDirectory = resolve(packageDirectory, 'dist');
const contentTypes = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.gif', 'image/gif'],
  ['.html', 'text/html; charset=utf-8'],
  ['.ico', 'image/x-icon'],
  ['.jpg', 'image/jpeg'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.webp', 'image/webp'],
  ['.woff', 'font/woff'],
  ['.woff2', 'font/woff2'],
]);

function option(name, fallback) {
  const index = process.argv.indexOf(name);
  return index === -1 ? fallback : process.argv[index + 1];
}

if (process.argv.includes('--help') || process.argv.includes('-h')) {
  console.log('Usage: npm start -- [--host <host>] [--port <port>]');
  console.log('Environment: MAATGEN_WEB_HOST, PORT');
  process.exit(0);
}

const host = option('--host', process.env.MAATGEN_WEB_HOST ?? '127.0.0.1');
const port = Number(option('--port', process.env.PORT ?? '5173'));
if (!host || !Number.isInteger(port) || port < 0 || port > 65535) {
  console.error('Invalid host or port. Use --help for usage.');
  process.exit(2);
}

function requestPath(requestUrl) {
  let pathname;
  try {
    pathname = decodeURIComponent(new URL(requestUrl ?? '/', 'http://localhost').pathname);
  } catch {
    return null;
  }
  const candidate = resolve(distDirectory, `.${normalize(pathname)}`);
  const rootRelative = relative(distDirectory, candidate);
  return rootRelative.startsWith('..') || rootRelative.includes(`..${'/'}`) ? null : candidate;
}

async function existingFile(path) {
  try {
    const details = await stat(path);
    return details.isFile() ? path : null;
  } catch {
    return null;
  }
}

const server = createServer(async (request, response) => {
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    response.writeHead(405, { Allow: 'GET, HEAD' });
    response.end();
    return;
  }

  const requestedPath = requestPath(request.url);
  const file = requestedPath && await existingFile(requestedPath);
  const fallback = file || (requestedPath && await existingFile(join(distDirectory, 'index.html')));
  if (!fallback) {
    response.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    response.end('Not found');
    return;
  }

  const type = file ? contentTypes.get(extname(file).toLowerCase()) : 'text/html; charset=utf-8';
  response.writeHead(200, { 'Content-Type': type ?? 'application/octet-stream' });
  if (request.method === 'HEAD') response.end();
  else createReadStream(fallback).pipe(response);
});

await access(distDirectory);
server.listen(port, host, () => {
  const address = server.address();
  const actualPort = typeof address === 'object' && address ? address.port : port;
  console.log(`Maatgen Web listening at http://${host}:${actualPort}/`);
});

function shutdown() {
  server.close(() => process.exit(0));
}
process.once('SIGINT', shutdown);
process.once('SIGTERM', shutdown);
