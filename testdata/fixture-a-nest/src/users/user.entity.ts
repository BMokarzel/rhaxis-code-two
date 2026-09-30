export class BaseEntity {
  id: string = '';
  createdAt: Date = new Date();
}

export class User extends BaseEntity {
  name: string;
  email: string;

  constructor(name: string, email: string) {
    super();
    this.name = name;
    this.email = email;
  }

  greet(): string {
    return `hi ${this.name}`;
  }
}
