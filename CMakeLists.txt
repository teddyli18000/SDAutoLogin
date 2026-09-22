# Build management for this project.
#
# CMake does not compile anything here: the Go toolchain is the compiler, and
# the task runner in tools/build owns the build flags. The targets below are a
# thin, familiar front end for it, so the command line, the wrapper scripts and
# CMake can never disagree about how the program is built.
#
#   cmake -S . -B build
#   cmake --build build                    # builds the program
#   cmake --build build --target check     # any other task of the runner
#   cmake --install build --prefix /usr/local
#
# The project declares no languages, so no C compiler is needed. On Windows that
# also means CMake needs a generator that does not depend on a C toolchain, for
# example Ninja: cmake -S . -B build -G Ninja

cmake_minimum_required(VERSION 3.20)

project(SDAutoLogin
	DESCRIPTION "Campus network auto login"
	HOMEPAGE_URL "https://git.zqlhub.com/zqlan/SDAutoLogin"
	LANGUAGES NONE)

find_program(GO_EXECUTABLE NAMES go REQUIRED
	DOC "The Go toolchain, which does the actual building")

if(WIN32)
	set(APP_BINARY "SDAutoLogin.exe")
else()
	set(APP_BINARY "SDAutoLogin")
endif()

# The target a plain "cmake --build build" runs. A custom target only joins the
# default build when it is declared ALL.
add_custom_target(SDAutoLogin ALL
	COMMAND "${GO_EXECUTABLE}" run ./tools/build build
	WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
	COMMENT "go run ./tools/build build"
	USES_TERMINAL
	VERBATIM)

# Every other task of the runner, opt in. The names all, test and clean belong
# to CMake itself, which is why the last three are called differently.
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
go_task(icon icon)
go_task(resources resources)
go_task(check test)
go_task(run run)
go_task(distclean clean)

install(PROGRAMS "${CMAKE_CURRENT_SOURCE_DIR}/dist/${APP_BINARY}"
	DESTINATION bin
	COMPONENT runtime)
