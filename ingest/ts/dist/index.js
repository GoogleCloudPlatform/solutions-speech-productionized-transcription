"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const socket_io_1 = require("socket.io");
const io = new socket_io_1.Server();
io.on("connection", (socket) => {
    console.log(socket.id);
});
io.listen(10000);
//# sourceMappingURL=index.js.map