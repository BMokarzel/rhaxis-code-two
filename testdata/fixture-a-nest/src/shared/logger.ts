export interface ILogger {
  log(message: string): void;
  error(message: string): void;
}

export abstract class BaseLogger implements ILogger {
  protected prefix: string;

  constructor(prefix: string) {
    this.prefix = prefix;
  }

  abstract log(message: string): void;

  error(message: string): void {
    console.error(`[${this.prefix}] ${message}`);
  }
}

export class AppLogger extends BaseLogger {
  constructor() {
    super('app');
  }

  log(message: string): void {
    console.log(`[${this.prefix}] ${message}`);
  }
}
