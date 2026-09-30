import { Injectable, NotFoundException } from '@nestjs/common';
import { AppLogger } from '@shared/logger';
import { CreateUserDto } from './dto/create-user.dto';
import { UsersRepository, UserStatus } from './users.repository';

@Injectable()
export class UsersService {
  private readonly logger = new AppLogger();

  constructor(private readonly repo: UsersRepository) {}

  async findOne(id: string) {
    const user = await this.repo.findById(id);
    if (!user) {
      throw new NotFoundException(`user ${id}`);
    }
    this.logger.log(user.email);
    return user;
  }

  async create(dto: CreateUserDto) {
    const status = UserStatus.Active;
    await this.repo.save(dto);
    return { ...dto, status };
  }
}
