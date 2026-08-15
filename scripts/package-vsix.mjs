import { readdir, readFile, stat, writeFile } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { argv, exit } from 'node:process';

const options = new Map();
for (let index = 2; index < argv.length; index += 2) options.set(argv[index], argv[index + 1]);
const extensionDirectory = options.get('--extension-dir');
const output = options.get('--output');
if (!extensionDirectory || !output) {
  console.error('Usage: node package-vsix.mjs --extension-dir <path> --output <path>');
  exit(2);
}

const files = [];
async function collect(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) await collect(path);
    else files.push(path);
  }
}
await collect(join(extensionDirectory, 'dist'));
await collect(join(extensionDirectory, 'media'));
files.push(join(extensionDirectory, 'package.json'));

const packageJson = JSON.parse(await readFile(join(extensionDirectory, 'package.json'), 'utf8'));
const xml = (value) => String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
const extensionId = packageJson.name.includes('/') ? packageJson.name.split('/').at(-1) : packageJson.name;
const manifest = `<?xml version="1.0" encoding="utf-8"?>
<PackageManifest Version="1.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011">
  <Metadata>
    <Identity Language="en-US" Id="${xml(packageJson.publisher + '.' + extensionId)}" Version="${xml(packageJson.version)}" />
    <DisplayName>${xml(packageJson.displayName)}</DisplayName>
    <Description xml:space="preserve">${xml(packageJson.description)}</Description>
    <GalleryFlags>Public</GalleryFlags>
    <Properties><Property Id="Microsoft.VisualStudio.Code.Engine" Value="${xml(packageJson.engines.vscode)}" /></Properties>
  </Metadata>
  <Installation><InstallationTarget Id="Microsoft.VisualStudio.Code" Version="0.10.0" /></Installation>
  <Dependencies />
  <Assets><Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" /></Assets>
</PackageManifest>`;
const contentTypes = `<?xml version="1.0" encoding="utf-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="vsixmanifest" ContentType="text/xml" />
  <Default Extension="json" ContentType="application/json" />
  <Default Extension="js" ContentType="application/javascript" />
  <Default Extension="css" ContentType="text/css" />
  <Default Extension="map" ContentType="application/json" />
  <Override PartName="/extension.vsixmanifest" ContentType="text/xml" />
</Types>`;

const entries = [
  { name: '[Content_Types].xml', data: Buffer.from(contentTypes) },
  { name: 'extension.vsixmanifest', data: Buffer.from(manifest) },
  ...await Promise.all(files.map(async (path) => ({
    name: `extension/${relative(extensionDirectory, path).replaceAll('\\', '/')}`,
    data: await readFile(path),
  }))),
];

function crc32(data) {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

const chunks = [];
const central = [];
let offset = 0;
for (const entry of entries) {
  const name = Buffer.from(entry.name);
  const crc = crc32(entry.data);
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(20, 4);
  local.writeUInt16LE(0x800, 6);
  local.writeUInt16LE(0, 8);
  local.writeUInt32LE(crc, 14);
  local.writeUInt32LE(entry.data.length, 18);
  local.writeUInt32LE(entry.data.length, 22);
  local.writeUInt16LE(name.length, 26);
  chunks.push(local, name, entry.data);

  const record = Buffer.alloc(46);
  record.writeUInt32LE(0x02014b50, 0);
  record.writeUInt16LE(20, 4);
  record.writeUInt16LE(20, 6);
  record.writeUInt16LE(0x800, 8);
  record.writeUInt32LE(crc, 16);
  record.writeUInt32LE(entry.data.length, 20);
  record.writeUInt32LE(entry.data.length, 24);
  record.writeUInt16LE(name.length, 28);
  record.writeUInt32LE(offset, 42);
  central.push(record, name);
  offset += local.length + name.length + entry.data.length;
}
const centralData = Buffer.concat(central);
const end = Buffer.alloc(22);
end.writeUInt32LE(0x06054b50, 0);
end.writeUInt16LE(entries.length, 8);
end.writeUInt16LE(entries.length, 10);
end.writeUInt32LE(centralData.length, 12);
end.writeUInt32LE(offset, 16);
await writeFile(output, Buffer.concat([...chunks, centralData, end]));
console.log(`Created ${output}`);
