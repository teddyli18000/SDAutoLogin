# CMake compiles nothing here: the Go toolchain is the compiler and the task
# runner in tools/build owns the build flags. The targets below are a front end
# for it.
#
#   cmake -S . -B build
#   cmake --build build                    # builds the program
#   cmake --build build --target check     # any other task of the runner
#   cmake --install build --prefix /usr/local
#
# The project declares no languages, so no C compiler is needed; on Windows that
# also means a generator that does not depend on a C toolchain, for example
# Ninja: cmake -S . -B build -G Ninja

cmake_minimum_required(VERSION 3.20)

project(SDAutoLogin
	DESCRIPTION "Campus network auto login"
	HOMEPAGE_URL "https://git.zqlhub.com/zqlan/SDAutoLogin"
	LANGUAGES NONE)

find_program(GO_EXECUTABLE NAMES go REQUIRED
	DOC "The Go toolchain")

if(WIN32)
	set(APP_BINARY "SDAutoLogin.exe")
else()
	set(APP_BINARY "SDAutoLogin")
endif()

add_custom_target(SDAutoLogin ALL
	COMMAND "${GO_EXECUTABLE}" run ./tools/build build
	WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
	COMMENT "go run ./tools/build build"
	USES_TERMINAL
	VERBATIM)

# Every other task of the runner, opt in. The names all, test and clean belong
# to CMake itself, so the last three are called differently.
function(go_task name task)
	add_custom_target(${name}
		COMMAND "${GO_EXECUTABLE}" run ./tools/build ${task} ${ARGN}
		WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
		COMMENT "go run ./tools/build ${task}"
		USES_TERMINAL
		VERBATIM)
endfunction()

# A console build for scripting, where the output must be visible and the exit
# code must reach the caller.
go_task(SDAutoLogin-console console)

go_task(all-platforms all)
go_task(packages packages)
go_task(verify verify)
go_task(icon icon)
go_task(resources resources)
go_task(check test)
go_task(run run)
go_task(distclean clean)

install(PROGRAMS "${CMAKE_CURRENT_SOURCE_DIR}/dist/${APP_BINARY}"
	DESTINATION bin
	COMPONENT runtime)
