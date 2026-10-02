import { Body, Controller, Get, Param, Post } from '@nestjs/common';
import { UsersService, UserInput } from '.';

@Controller('users')
export class UsersController {
  constructor(private readonly usersService: UsersService) {}

  @Get(':id')
  getUser(@Param('id') id: string) {
    return this.usersService.findOne(id);
  }

  @Post()
  create(@Body() body: UserInput) {
    return this.usersService.create(body);
  }
}
