const { readFile } = require('node:fs/promises');
const { double } = require('./utils');
const { LRUCache } = require('./cache');

const cache = new LRUCache(100);

async function loadConfig(path) {
  const cached = cache.get(path);
  if (cached) return cached;
  const raw = await readFile(path, 'utf8');
  const parsed = JSON.parse(raw);
  cache.set(path, parsed);
  return parsed;
}

function transform(items) {
  return items.map(double);
}

module.exports = { loadConfig, transform };
