import { Injectable } from '@nestjs/common';
import { CreateUserDto } from './dto/create-user.dto';

export enum UserStatus {
  Active = 'active',
  Blocked = 'blocked',
}

@Injectable()
export class UsersRepository {
  private readonly users = new Map<string, CreateUserDto>();

  async findById(id: string): Promise<CreateUserDto | undefined> {
    return this.users.get(id);
  }

  async save(dto: CreateUserDto) {
    this.users.set(dto.email, dto);
  }
}
