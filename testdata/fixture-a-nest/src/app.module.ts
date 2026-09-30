import { UsersController } from './users/users.controller';
import { UsersService } from './users/users.service';
import { UsersRepository } from './users/users.repository';

export class AppModule {
  static controllers = [UsersController];
  static providers = [UsersService, UsersRepository];
}
