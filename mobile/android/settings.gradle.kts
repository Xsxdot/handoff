pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    // 仓库集中在 settings 声明，模块脚本不得再声明仓库。
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        // 本地 gomobile 产物：由 ../../mobile/build.sh android 落 dist/mobile/handoff-mobile.aar。
        flatDir { dirs(rootDir.resolve("../../dist/mobile")) }
    }
}

rootProject.name = "handoff-mobile-android"
include(":app")
