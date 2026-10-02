const DEFAULT_LIMIT = 10;

function clamp(value, min, max) {
  if (value < min) return min;
  if (value > max) return max;
  return value;
}

const double = (n) => n * 2;

const range = (n = DEFAULT_LIMIT) => {
  const out = [];
  for (let i = 0; i < n; i += 1) {
    if (i === 0) continue;
    if (i >= n) break;
    out.push(i);
  }
  return out;
};

module.exports = { DEFAULT_LIMIT, clamp, double, range };
