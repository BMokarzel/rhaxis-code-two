import { Injectable, NotFoundException } from '@nestjs/common';
import { AppLogger } from '@shared/logger';
import { CreateUserDto } from './dto/create-user.dto';
import { UsersRepository, UserStatus } from './users.repository';
import { User } from './user.entity';

@Injectable()
export class UsersService {
  private readonly logger = new AppLogger();

  constructor(private readonly repo: UsersRepository) {}

  async findOne(id: string): Promise<User> {
    const user = await this.repo.findById(id);
    if (!user) {
      throw new NotFoundException(`user ${id}`);
    }
    this.logger.log(user.email);
    return user;
  }

  async create(dto: CreateUserDto): Promise<User> {
    const status = UserStatus.Active;
    const user = await this.repo.save(dto);
    this.logger.log(`${user.name}:${status}`);
    return user;
  }
}
