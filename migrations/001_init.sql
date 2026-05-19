/*
考虑这么一个场景，用户对家具说“开灯”，Go Server 需要知道：这个用户是谁？（user_id），这个用户的设备有哪些？（查 devices 表 WHERE owner_id = ?）
客厅灯的设备 ID 是什么？（device_id = "light-living-room-001"），通过 MQTT 给这个设备发指令
对话结束后：这次的语音会话存下来（conversations 表） → 每条消息存下来（messages 表）
*/

CREATE TABLE IF NOT EXISTS users(
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, -- 存用户id：无符号自动递增主键
    phone VARCHAR(20) NOT NULL DEFAULT '', -- 存用户电话号码，不能为空，默认为空字符串
    email VARCHAR(100) NOT NULL DEFAULT '',
    password VARCHAR(100) NOT NULL COMMENT 'bcrypt hash', -- 存密码：密码存的是 bcrypt 加密后的 hash，不是明文密码
    nickname   VARCHAR(50)  NOT NULL DEFAULT '',
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP, --注册时间
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, -- CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP:只要这行数据被修改，这一行自动更新为当前时间
    UNIQUE KEY uk_phone (phone), -- UNIQUE KEY表示唯一约束，一个手机号只能注册一个账号
    UNIQUE KEY uk_email (email) -- 同理
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4; -- 设置存储引擎和默认编码

CREATE TABLE IF NOT EXISTS devices (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    device_id     VARCHAR(64)  NOT NULL COMMENT 'MQTT client id', -- 每个设备必须有的唯一编号，用于MQTT
    owner_id      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'FK → users.id', -- 归属用户id，FK表示foreign key外键，指向users.id
    type          VARCHAR(20)  NOT NULL DEFAULT '' COMMENT 'light/aircon/curtain/socket', -- 设备类型，支持注释的四种
    name          VARCHAR(50)  NOT NULL DEFAULT '',
    room          VARCHAR(50)  NOT NULL DEFAULT '',
    status        VARCHAR(20)  NOT NULL DEFAULT 'offline' COMMENT 'online/offline/error', -- 设备状态
    bound_at      DATETIME     NULL,
    last_online_at DATETIME    NULL,
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_device_id (device_id),
    INDEX idx_owner (owner_id), -- 给owner_id建索引，用于速查这个owner有哪些设备
    INDEX idx_status (status) -- 给status建索引，用于速查各个设备的当前状态
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

/*
  - device_id 用 VARCHAR 而不是 BIGINT，因为 MQTT client id 是字符串（比如 light-001），不用自增 ID 做设备标识——这是 IoT 系统的惯例，设备自带唯一 ID。    
  - owner_id 不是外键约束，只是逻辑外键。真实业务中设备量一大，物理外键的问题：
    引发锁表：只要有外键关联，删除 / 修改用户数据时，数据库要遍历校验所有关联设备，设备越多、数据量越大，校验越慢，直接阻塞读写，高并发场景直接拖垮性能
    级联风险：一旦设置级联删除，删用户直接批量删设备，极易误删业务数据
    分库分表、数据迁移极度麻烦，后期业务拆分库表，外键约束会成为巨大阻碍
  - status 用 VARCHAR 而不是 ENUM——ENUM 加新值要 ALTER TABLE，VARCHAR 更灵活。
  - utf8mb4 是真正的 UTF-8（支持 emoji）。
  - Docker 的 MySQL 容器第一次启动时，会自动执行 docker-entrypoint-initdb.d 目录下所有 .sql 文件—— docker-compose.yml 里的
  ./migrations:/docker-entrypoint-initdb.d 挂载就是这个用途。
*/