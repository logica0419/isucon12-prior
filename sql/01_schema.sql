USE `isucon2021_prior`;

DROP TABLE IF EXISTS `users`;

CREATE TABLE `users` (
  `id` VARCHAR(26) PRIMARY KEY NOT NULL,
  `email` VARCHAR(255) NOT NULL DEFAULT '',
  `nickname` VARCHAR(120) NOT NULL DEFAULT '',
  `staff` BOOLEAN NOT NULL DEFAULT false,
  `created_at` DATETIME(6) NOT NULL
) ENGINE = InnoDB DEFAULT CHARACTER SET = utf8mb4;

DROP TABLE IF EXISTS `schedules`;

CREATE TABLE `schedules` (
  `id` VARCHAR(26) PRIMARY KEY NOT NULL,
  `title` VARCHAR(255) NOT NULL DEFAULT '',
  `capacity` INT UNSIGNED NOT NULL DEFAULT 0,
  `created_at` DATETIME(6) NOT NULL
) ENGINE = InnoDB DEFAULT CHARACTER SET = utf8mb4;

DROP TABLE IF EXISTS `reservations`;

CREATE TABLE `reservations` (
  `id` VARCHAR(26) NOT NULL,
  `schedule_id` VARCHAR(26) NOT NULL,
  `user_id` VARCHAR(26) NOT NULL,
  `created_at` DATETIME(6) NOT NULL,
  PRIMARY KEY (`schedule_id`, `user_id`)
) ENGINE = InnoDB DEFAULT CHARACTER SET = utf8mb4;