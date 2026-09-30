import { Injectable } from '@nestjs/common';
import { CreateUserDto } from './dto/create-user.dto';
import { User } from './user.entity';

export enum UserStatus {
  Active = 'active',
  Blocked = 'blocked',
  Pending = 'pending',
}

@Injectable()
export class UsersRepository {
  private readonly users = new Map<string, User>();

  async findById(id: string): Promise<User | undefined> {
    return this.users.get(id);
  }

  async save(dto: CreateUserDto): Promise<User> {
    const user = new User(dto.name, dto.email);
    this.users.set(user.id, user);
    return user;
  }
}
