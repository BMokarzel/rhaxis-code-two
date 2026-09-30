const { loadConfig, transform } = require('./lib/client');
const { clamp, range } = require('./lib/utils');

async function main() {
  const config = await loadConfig('./config.json');
  const size = clamp(config.size, 1, 100);
  const items = range(size);
  const result = transform(items);
  console.log(result);
  return result;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

module.exports = { main };
