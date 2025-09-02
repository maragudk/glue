create table products (id text primary key, name text not null, price real not null) strict;
insert into products (id, name, price) values ('prod1', 'Widget A', 10.50);
insert into products (id, name, price) values ('prod2', 'Widget B', 20.00);
