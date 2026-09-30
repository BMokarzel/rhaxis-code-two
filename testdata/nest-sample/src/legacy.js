const { readFile } = require('node:fs/promises');

const CONFIG = { path: './data.json', retries: 3 };

function greet(name) {
  return `hello, ${name}`;
}

const double = (n) => n * 2;

class Counter {
  constructor(start = 0) {
    this.value = start;
  }

  inc() {
    this.value += 1;
    return this.value;
  }
}

async function loadUsers() {
  const c = new Counter();
  let raw;
  try {
    raw = await readFile(CONFIG.path, 'utf8');
  } catch (err) {
    throw err;
  }
  const users = JSON.parse(raw);
  for (const u of users) {
    c.inc();
    console.log(greet(u.name));
  }
  return users.map(double);
}

module.exports = { loadUsers };
