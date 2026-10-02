const { loadConfig, transform } = require('./lib/client');
const { clamp, range } = require('./lib/utils');
const kafka = require('./lib/kafka');
const sqs = require('./lib/sqs');

async function main() {
  const base = process.env.USERS_URL || 'http://users:8080';
  const config = await loadConfig(base + '/config.json');
  const size = clamp(config.size, 1, 100);
  const items = range(size);
  const result = transform(items);
  kafka.send('orders.created', result);
  sqs.sendMessage({ QueueUrl: 'https://sqs.us-east-1.amazonaws.com/123/jobs-queue', MessageBody: JSON.stringify(result) });
  console.log(result);
  return result;
}

async function worker() {
  const consumer = kafka.consumer();
  consumer.subscribe('user.created', (msg) => {
    console.log('received user', msg);
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

module.exports = { main, worker };
