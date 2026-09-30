class Store {
  constructor() {
    this.data = new Map();
  }

  set(key, value) {
    this.data.set(key, value);
  }

  get(key) {
    return this.data.get(key);
  }
}

class LRUCache extends Store {
  constructor(size) {
    super();
    this.size = size;
    this.order = [];
  }

  set(key, value) {
    if (this.order.length >= this.size) {
      const evict = this.order.shift();
      this.data.delete(evict);
    }
    this.order.push(key);
    super.set(key, value);
  }
}

module.exports = { Store, LRUCache };
